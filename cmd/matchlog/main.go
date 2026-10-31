// Command matchlog imports published match logs and reports their recorded events.
package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
	"unicode/utf16"
)

type event struct {
	Version  int                 `json:"schema_version"`
	ID       string              `json:"match_id"`
	Sequence int                 `json:"sequence"`
	UTC      string              `json:"utc"`
	Elapsed  *int64              `json:"elapsed_ns"`
	Type     string              `json:"event_type"`
	Timing   map[string]*float64 `json:"timing_ms"`
	Result   *string             `json:"result"`
	Error    json.RawMessage     `json:"error"`
	Manual   *bool               `json:"manual_intervention"`
}
type report struct {
	MatchID             string               `json:"match_id"`
	Status              string               `json:"status"`
	Outcome             string               `json:"outcome"`
	Events              int                  `json:"events"`
	Counts              map[string]int       `json:"event_counts"`
	Timings             map[string][]float64 `json:"timing_ms_samples"`
	Failures            int                  `json:"failures"`
	Timeouts            int                  `json:"timeouts"`
	ManualInterventions int                  `json:"manual_interventions"`
}

func parse(data []byte) (report, error) {
	r := report{Status: "incomplete", Outcome: "unknown", Counts: map[string]int{}, Timings: map[string][]float64{}}
	if len(data) == 0 || data[len(data)-1] != '\n' {
		return r, errors.New("empty or unterminated JSONL")
	}
	s := bufio.NewScanner(bytes.NewReader(data))
	s.Buffer(make([]byte, 4096), 4<<20)
	var previous int64 = -1
	for s.Scan() {
		var e event
		if err := json.Unmarshal(s.Bytes(), &e); err != nil {
			return r, fmt.Errorf("line %d: %w", r.Events+1, err)
		}
		if e.Version != 1 || e.ID == "" || e.Type == "" || e.Sequence != r.Events+1 {
			return r, fmt.Errorf("line %d: invalid version, identity, type or sequence", r.Events+1)
		}
		if r.IDMismatch(e.ID) {
			return r, errors.New("multiple match IDs")
		}
		r.MatchID = e.ID
		if _, err := time.Parse(time.RFC3339Nano, e.UTC); err != nil {
			return r, fmt.Errorf("line %d: invalid utc", e.Sequence)
		}
		if e.Elapsed == nil || *e.Elapsed < 0 || *e.Elapsed < previous {
			return r, fmt.Errorf("line %d: invalid elapsed_ns", e.Sequence)
		}
		previous = *e.Elapsed
		if r.Status != "incomplete" || (r.Events > 0 && (r.Counts["match_incomplete"] > 0)) {
			return r, errors.New("event after terminal")
		}
		r.Events++
		r.Counts[e.Type]++
		for k, v := range e.Timing {
			if v == nil {
				continue
			}
			if math.IsNaN(*v) || math.IsInf(*v, 0) || *v < 0 {
				return r, fmt.Errorf("invalid timing %s", k)
			}
			r.Timings[k] = append(r.Timings[k], *v)
		}
		if len(e.Error) > 0 && string(e.Error) != "null" && string(e.Error) != "\"\"" {
			r.Failures++
			if strings.Contains(strings.ToLower(string(e.Error)), "timeout") {
				r.Timeouts++
			}
		}
		if e.Manual != nil && *e.Manual {
			r.ManualInterventions++
		}
		if e.Type == "match_completed" || e.Type == "match_incomplete" {
			r.Status = e.Type
			if e.Result != nil {
				switch *e.Result {
				case "win", "loss", "draw", "unknown":
					r.Outcome = *e.Result
				default:
					return r, errors.New("invalid outcome")
				}
			}
		}
	}
	if err := s.Err(); err != nil {
		return r, err
	}
	if r.Status == "incomplete" {
		return r, errors.New("missing terminal event")
	}
	return r, nil
}
func (r report) IDMismatch(id string) bool { return r.MatchID != "" && r.MatchID != id }

type remoteItem struct {
	Name     string `json:"name"`
	Complete bool   `json:"complete"`
	Data     string `json:"data"`
	Error    string `json:"error"`
}

// PowerShell receives the explicit source path encoded as data, not executable syntax.
func fetch(ctx context.Context, host, source string) ([]remoteItem, error) {
	script := `$ErrorActionPreference='Stop'; $p=[Text.Encoding]::UTF8.GetString([Convert]::FromBase64String('` + base64.StdEncoding.EncodeToString([]byte(source)) + `')); Get-ChildItem -LiteralPath $p -Directory | ForEach-Object { $d=$_; $m=Join-Path $d.FullName 'COMPLETE'; $f=Join-Path $d.FullName 'events.jsonl'; $o=@{name=$d.Name;complete=(Test-Path -LiteralPath $m -PathType Leaf);data='';error=''}; if($o.complete) { try { $i=Get-Item -LiteralPath $f; if($i.Length -gt 16777216){throw 'log exceeds 16 MiB'}; $o.data=[Convert]::ToBase64String([IO.File]::ReadAllBytes($f)) } catch { $o.error=$_.Exception.Message } }; $o | ConvertTo-Json -Compress }`
	words := utf16.Encode([]rune(script))
	raw := make([]byte, len(words)*2)
	for i, w := range words {
		raw[2*i] = byte(w)
		raw[2*i+1] = byte(w >> 8)
	}
	cmd := exec.CommandContext(ctx, "ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=10", host, "powershell", "-NoProfile", "-NonInteractive", "-EncodedCommand", base64.StdEncoding.EncodeToString(raw))
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("ssh retrieval: %w", err)
	}
	var items []remoteItem
	s := bufio.NewScanner(bytes.NewReader(out))
	s.Buffer(make([]byte, 4096), 24<<20)
	for s.Scan() {
		var item remoteItem
		if err := json.Unmarshal(s.Bytes(), &item); err != nil {
			return nil, fmt.Errorf("invalid remote response: %w", err)
		}
		items = append(items, item)
	}
	return items, s.Err()
}
func importItems(dir string, items []remoteItem, output io.Writer) error {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	var failures int
	for _, item := range items {
		if !item.Complete {
			fmt.Fprintf(output, "unpublished directory: %s\n", item.Name)
			continue
		}
		if item.Error != "" {
			fmt.Fprintf(output, "%s: %s\n", item.Name, item.Error)
			failures++
			continue
		}
		data, err := base64.StdEncoding.DecodeString(item.Data)
		if err != nil {
			fmt.Fprintf(output, "%s: invalid encoding\n", item.Name)
			failures++
			continue
		}
		r, err := parse(data)
		if err != nil {
			fmt.Fprintf(output, "%s: %v\n", item.Name, err)
			failures++
			continue
		}
		if r.MatchID != item.Name || item.Name == "." || item.Name == ".." || strings.ContainsAny(item.Name, `/\`) {
			fmt.Fprintf(output, "%s: directory and match ID mismatch or unsafe name\n", item.Name)
			failures++
			continue
		}
		path := filepath.Join(dir, item.Name+".jsonl")
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if errors.Is(err, os.ErrExist) {
			old, e := os.ReadFile(path)
			if e != nil || !bytes.Equal(old, data) {
				fmt.Fprintf(output, "%s: conflicting existing match\n", item.Name)
				failures++
			} else {
				fmt.Fprintf(output, "%s: already imported\n", item.Name)
			}
			continue
		}
		if err != nil {
			fmt.Fprintf(output, "%s: %v\n", item.Name, err)
			failures++
			continue
		}
		_, err = f.Write(data)
		if err == nil {
			err = f.Sync()
		}
		closeErr := f.Close()
		if err == nil {
			err = closeErr
		}
		if err != nil {
			fmt.Fprintf(output, "%s: write failed: %v (inspect file)\n", item.Name, err)
			failures++
			continue
		}
		sum := sha256.Sum256(data)
		fmt.Fprintf(output, "%s: imported sha256=%s\n", item.Name, hex.EncodeToString(sum[:]))
		if err := json.NewEncoder(output).Encode(r); err != nil {
			return err
		}
	}
	if failures > 0 {
		return fmt.Errorf("%d imports failed", failures)
	}
	return nil
}
func analyze(paths []string, out io.Writer) error {
	sort.Strings(paths)
	seen := map[string][]byte{}
	enc := json.NewEncoder(out)
	var failures int
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", p, err)
			failures++
			continue
		}
		r, err := parse(data)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", p, err)
			failures++
			continue
		}
		if old, ok := seen[r.MatchID]; ok {
			if !bytes.Equal(old, data) {
				fmt.Fprintf(os.Stderr, "%s: conflicting match ID %s\n", p, r.MatchID)
				failures++
			} else {
				fmt.Fprintf(os.Stderr, "%s: duplicate match ID %s\n", p, r.MatchID)
				failures++
			}
			continue
		}
		seen[r.MatchID] = data
		if err = enc.Encode(r); err != nil {
			return err
		}
	}
	if failures > 0 {
		return fmt.Errorf("%d logs failed analysis", failures)
	}
	return nil
}
func run() error {
	if len(os.Args) < 2 {
		return errors.New("usage: matchlog analyze FILE... | import -source WINDOWS_PATH -dest LOCAL_DIR [-host redmi-gpro] [-watch 30s]")
	}
	switch os.Args[1] {
	case "analyze":
		if len(os.Args) < 3 {
			return errors.New("analyze requires files")
		}
		return analyze(os.Args[2:], os.Stdout)
	case "import":
		fs := flag.NewFlagSet("import", flag.ContinueOnError)
		source := fs.String("source", "", "explicit Windows source directory")
		dest := fs.String("dest", "", "local destination")
		host := fs.String("host", "redmi-gpro", "existing SSH host")
		watch := fs.Duration("watch", 0, "poll interval; zero imports once")
		if err := fs.Parse(os.Args[2:]); err != nil {
			return err
		}
		if *source == "" || *dest == "" || *watch < 0 || strings.HasPrefix(*host, "-") {
			return errors.New("source and dest required; invalid host or interval")
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		for {
			request, cancel := context.WithTimeout(ctx, 30*time.Second)
			items, err := fetch(request, *host, *source)
			cancel()
			if err == nil {
				err = importItems(*dest, items, os.Stdout)
			}
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				if *watch == 0 {
					return err
				}
			}
			if *watch == 0 {
				return nil
			}
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(*watch):
			}
		}
	default:
		return errors.New("unknown command")
	}
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
