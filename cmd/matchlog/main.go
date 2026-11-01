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
	"sync/atomic"
	"syscall"
	"time"
	"unicode/utf16"

	"gwentcoachmvp/internal/matchlog"
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
	s.Buffer(make([]byte, 4096), 16<<20+1)
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
		if len(e.Error) > 0 && string(e.Error) != "null" {
			r.Failures++
			var text string
			if json.Unmarshal(e.Error, &text) == nil && strings.Contains(strings.ToLower(text), "timeout") {
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
	Name     string  `json:"name"`
	Complete bool    `json:"complete"`
	Data     string  `json:"data"`
	Training *string `json:"training"`
	Error    string  `json:"error"`
}

type progressReader struct {
	io.Reader
	progress chan<- struct{}
	latest   *atomic.Int64
	start    time.Time
}

func (r progressReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	if n > 0 {
		r.latest.Store(time.Since(r.start).Nanoseconds())
		select {
		case r.progress <- struct{}{}:
		default:
		}
	}
	return n, err
}

// PowerShell receives the explicit source path encoded as data, not executable syntax.
func fetch(ctx context.Context, host, source string, timeout time.Duration, consume func(remoteItem) error) error {
	if timeout <= 0 {
		return errors.New("progress timeout must be positive")
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	script := `$ErrorActionPreference='Stop'; $p=[Text.Encoding]::UTF8.GetString([Convert]::FromBase64String('` + base64.StdEncoding.EncodeToString([]byte(source)) + `')); Get-ChildItem -LiteralPath $p -Directory | ForEach-Object { $d=$_; $m=Join-Path $d.FullName 'COMPLETE'; $f=Join-Path $d.FullName 'events.jsonl'; $t=Join-Path $d.FullName 'training.jsonl'; $o=@{name=$d.Name;complete=(Test-Path -LiteralPath $m -PathType Leaf);data='';training=$null;error=''}; if($o.complete) { try { if ((Get-Item -LiteralPath $m).Length -ne 0) { throw 'COMPLETE marker is not empty' }; $i=Get-Item -LiteralPath $f; if($i.Length -gt 16777216){throw 'log exceeds 16 MiB'}; $o.data=[Convert]::ToBase64String([IO.File]::ReadAllBytes($f)); if(Test-Path -LiteralPath $t -PathType Leaf){ $i=Get-Item -LiteralPath $t; if($i.Length -gt 16777216){throw 'training data exceeds 16 MiB'}; $o.training=[Convert]::ToBase64String([IO.File]::ReadAllBytes($t)) } } catch { $o.error=$_.Exception.Message } }; $o | ConvertTo-Json -Compress }`
	words := utf16.Encode([]rune(script))
	raw := make([]byte, len(words)*2)
	for i, w := range words {
		raw[2*i] = byte(w)
		raw[2*i+1] = byte(w >> 8)
	}
	cmd := exec.CommandContext(ctx, "ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=10", "-o", "ServerAliveInterval=15", "-o", "ServerAliveCountMax=2", host, "powershell", "-NoProfile", "-NonInteractive", "-EncodedCommand", base64.StdEncoding.EncodeToString(raw))
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = os.Stderr
	if err = cmd.Start(); err != nil {
		return err
	}
	progress := make(chan struct{}, 1)
	done := make(chan struct{})
	stopped := make(chan struct{})
	start := time.Now()
	var latest atomic.Int64
	var timedOut atomic.Bool
	go func() {
		defer close(stopped)
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				_ = pipe.Close()
				return
			case <-progress:
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				timer.Reset(timeout)
			case <-timer.C:
				remaining := timeout - (time.Since(start) - time.Duration(latest.Load()))
				if remaining > 0 {
					timer.Reset(remaining)
					continue
				}
				timedOut.Store(true)
				cancel()
				_ = pipe.Close()
				return
			}
		}
	}()
	s := bufio.NewScanner(progressReader{Reader: pipe, progress: progress, latest: &latest, start: start})
	s.Buffer(make([]byte, 4096), 48<<20)
	var failures int
	for s.Scan() {
		var item remoteItem
		if err := json.Unmarshal(s.Bytes(), &item); err != nil {
			fmt.Fprintf(os.Stderr, "invalid remote response: %v\n", err)
			failures++
			continue
		}
		if err := consume(item); err != nil {
			fmt.Fprintln(os.Stderr, err)
			failures++
		}
	}
	scanErr := s.Err()
	if scanErr != nil {
		_ = cmd.Process.Kill()
	}
	waitErr := cmd.Wait()
	close(done)
	<-stopped
	if timedOut.Load() {
		return fmt.Errorf("ssh retrieval: no output progress for %s", timeout)
	}
	if scanErr != nil {
		return scanErr
	}
	if waitErr != nil {
		return fmt.Errorf("ssh retrieval: %w", waitErr)
	}
	if failures > 0 {
		return fmt.Errorf("%d remote items failed", failures)
	}
	return nil
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
		if item.Training != nil {
			if err := importTraining(dir, item, data); err != nil {
				fmt.Fprintf(output, "%s: %v\n", item.Name, err)
				failures++
				continue
			}
			sum := sha256.Sum256(data)
			fmt.Fprintf(output, "%s: imported sealed directory events_sha256=%s\n", item.Name, hex.EncodeToString(sum[:]))
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
func importTraining(root string, item remoteItem, events []byte) error {
	training, err := base64.StdEncoding.DecodeString(*item.Training)
	if err != nil {
		return fmt.Errorf("invalid training encoding: %w", err)
	}
	staging, err := os.MkdirTemp(root, ".match-import-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(staging)
	if err = os.WriteFile(filepath.Join(staging, "events.jsonl"), events, 0600); err != nil {
		return err
	}
	parsed, err := matchlog.Read(filepath.Join(staging, "events.jsonl"))
	if err != nil {
		return err
	}
	if err = matchlog.ValidateResultClaims(parsed); err != nil {
		return err
	}
	if err = matchlog.ExportConfirmedTraining(filepath.Join(staging, "training.jsonl"), parsed); err != nil {
		return err
	}
	derived, err := os.ReadFile(filepath.Join(staging, "training.jsonl"))
	if err != nil {
		return err
	}
	if !bytes.Equal(derived, training) {
		return fmt.Errorf("training export differs from confirmed events")
	}
	if err = os.WriteFile(filepath.Join(staging, "COMPLETE"), nil, 0600); err != nil {
		return err
	}
	dest := filepath.Join(root, item.Name)
	if _, err = os.Lstat(dest); err == nil {
		for name, want := range map[string][]byte{"events.jsonl": events, "training.jsonl": training, "COMPLETE": {}} {
			got, e := os.ReadFile(filepath.Join(dest, name))
			if e != nil || !bytes.Equal(got, want) {
				return fmt.Errorf("conflicting existing sealed match")
			}
		}
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	return os.Rename(staging, dest)
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
		progressTimeout := fs.Duration("progress-timeout", 2*time.Minute, "maximum time without SSH output")
		if err := fs.Parse(os.Args[2:]); err != nil {
			return err
		}
		if *source == "" || *dest == "" || *watch < 0 || *progressTimeout <= 0 || strings.HasPrefix(*host, "-") {
			return errors.New("source and dest required; invalid host or interval")
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		for {
			err := fetch(ctx, *host, *source, *progressTimeout, func(item remoteItem) error {
				return importItems(*dest, []remoteItem{item}, os.Stdout)
			})
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
