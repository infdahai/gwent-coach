package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf16"
)

type pulledRun struct {
	Name string `json:"name"`
	Data []byte `json:"data"`
}

type limitedBuffer struct{ bytes.Buffer }

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > 64<<20 {
		return 0, fmt.Errorf("remote collection response exceeds 64 MiB")
	}
	return b.Buffer.Write(p)
}

func runPull(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("pull", flag.ContinueOnError)
	host := fs.String("host", "", "SSH host")
	source := fs.String("source", "", "Windows collection root")
	dest := fs.String("dest", "", "private local collection inbox")
	interval := fs.Duration("watch", 0, "poll interval; zero runs once")
	timeout := fs.Duration("timeout", 2*time.Minute, "maximum duration of one pull")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 || *host == "" || strings.HasPrefix(*host, "-") || *source == "" || *dest == "" || *interval < 0 || *timeout <= 0 {
		return fmt.Errorf("usage: collection-analysis pull -host HOST -source WINDOWS_DIR -dest LOCAL_DIR [-watch 30s] [-timeout 2m]")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	for {
		err := pullOnce(ctx, *host, *source, *dest, *timeout, out)
		if *interval == 0 {
			return err
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "collection pull failed: %v\n", err)
		}
		timer := time.NewTimer(*interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
}

func pullOnce(ctx context.Context, host, source, dest string, timeout time.Duration, out io.Writer) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	// The path is encoded as data; the script performs only directory and file reads.
	script := `$ErrorActionPreference='Stop'; $ProgressPreference='SilentlyContinue'; [Console]::OutputEncoding=[Text.UTF8Encoding]::new($false); $p=[Text.Encoding]::UTF8.GetString([Convert]::FromBase64String('` + base64.StdEncoding.EncodeToString([]byte(source)) + `')); Get-ChildItem -LiteralPath $p -Directory | Where-Object { $_.Name -like 'readonly-collection-*' } | ForEach-Object { $f=Join-Path $_.FullName 'recognition.jsonl'; if(Test-Path -LiteralPath $f -PathType Leaf) { $s=[IO.File]::Open($f,[IO.FileMode]::Open,[IO.FileAccess]::Read,[IO.FileShare]::ReadWrite -bor [IO.FileShare]::Delete); try { $n=$s.Length; if($n -gt 16777216) { throw 'collection exceeds 16 MiB' }; $data=New-Object byte[] $n; $offset=0; while($offset -lt $n) { $read=$s.Read($data,$offset,$n-$offset); if($read -eq 0) { throw 'collection truncated during read' }; $offset+=$read }; @{name=$_.Name;data=[Convert]::ToBase64String($data)} | ConvertTo-Json -Compress } finally { $s.Dispose() } } }`
	words := utf16.Encode([]rune(script))
	raw := make([]byte, len(words)*2)
	for i, w := range words {
		raw[2*i] = byte(w)
		raw[2*i+1] = byte(w >> 8)
	}
	cmd := exec.CommandContext(ctx, "ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=10", "-o", "ServerAliveInterval=15", "-o", "ServerAliveCountMax=2", host, "powershell", "-NoProfile", "-NonInteractive", "-EncodedCommand", base64.StdEncoding.EncodeToString(raw))
	var output limitedBuffer
	cmd.Stdout = &output
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("collection pull: %w", ctx.Err())
		}
		return fmt.Errorf("collection pull: %w", err)
	}
	return processPulled(bytes.NewReader(output.Bytes()), dest, out)
}

func processPulled(input io.Reader, dest string, out io.Writer) error {
	root, err := os.MkdirTemp("", "gwent-collection-pull-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(root)
	dec := json.NewDecoder(input)
	seen := map[string]bool{}
	for {
		var item pulledRun
		err := dec.Decode(&item)
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("invalid remote collection: %w", err)
		}
		if !strings.HasPrefix(item.Name, "readonly-collection-") || filepath.Base(item.Name) != item.Name || strings.ContainsAny(item.Name, `\/`) || seen[item.Name] || len(item.Data) > 16<<20 {
			return fmt.Errorf("invalid remote run name or data")
		}
		seen[item.Name] = true
		dir := filepath.Join(root, item.Name)
		if err := os.Mkdir(dir, 0700); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, "recognition.jsonl"), item.Data, 0600); err != nil {
			return err
		}
	}
	if err := importCollections(root, dest, out); err != nil {
		return err
	}
	for name := range seen {
		f, err := os.Open(filepath.Join(dest, name+".jsonl"))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		result, err := analyze(f)
		f.Close()
		if err != nil {
			return err
		}
		if err := json.NewEncoder(out).Encode(struct {
			Run     string  `json:"run"`
			Summary summary `json:"summary"`
		}{name, result}); err != nil {
			return err
		}
	}
	return nil
}
