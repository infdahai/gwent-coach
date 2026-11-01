package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf16"
)

func TestPullOnceWithSSHTransport(t *testing.T) {
	dir := t.TempDir()
	ssh := filepath.Join(dir, "ssh")
	response := `{"name":"readonly-collection-1","data":"eyJ0cnVzdCI6InVudmVyaWZpZWRfY2FuZGlkYXRlX25vdF9mb3JfYWN0aW9ucyIsInJlZmVyZW5jZSI6ImZyYW1lIiwiY2FwdHVyZWRfYXQiOiIyMDI2LTAxLTAxVDAwOjAwOjAwWiIsInNjYWxhcnMiOnt9LCJ1bmtub3duIjpbXX0K"}`
	if err := os.WriteFile(ssh, []byte("#!/bin/sh\nfor arg do last=$arg; done\nprintf '%s' \"$last\" > \"$PULL_SCRIPT_CAPTURE\"\nprintf '%s\\n' '"+response+"'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	capture := filepath.Join(dir, "script")
	t.Setenv("PULL_SCRIPT_CAPTURE", capture)
	var out bytes.Buffer
	if err := pullOnce(context.Background(), "test-host", `D:\runs`, dir, time.Second, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), candidateTrust) {
		t.Fatal(out.String())
	}
	encoded, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := base64.StdEncoding.DecodeString(string(encoded))
	if err != nil || len(raw)%2 != 0 {
		t.Fatalf("invalid encoded script: %v", err)
	}
	words := make([]uint16, len(raw)/2)
	for i := range words {
		words[i] = binary.LittleEndian.Uint16(raw[i*2:])
	}
	script := string(utf16.Decode(words))
	if !strings.Contains(script, "[IO.FileShare]::ReadWrite") || !strings.Contains(script, "$n=$s.Length") {
		t.Fatal("script must use shared snapshot reads")
	}
	if err := os.WriteFile(ssh, []byte("#!/bin/sh\nexit 7\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := pullOnce(context.Background(), "test-host", `D:\runs`, dir, time.Second, &out); err == nil {
		t.Fatal("accepted failed transport")
	}
}

func TestPulledRunsIsolateFailuresAndLargeStreams(t *testing.T) {
	dest := t.TempDir()
	line := `{"trust":"unverified_candidate_not_for_actions","reference":"frame","captured_at":"2026-01-01T00:00:00Z","scalars":{},"unknown":[]}` + "\n"
	if err := os.WriteFile(filepath.Join(dest, "readonly-collection-conflict.jsonl"), []byte(line+line), 0600); err != nil {
		t.Fatal(err)
	}
	var stream bytes.Buffer
	encoder := json.NewEncoder(&stream)
	for _, item := range []pulledRun{{Name: "readonly-collection-oversize", Error: "collection exceeds 16 MiB"}, {Name: "readonly-collection-conflict", Data: []byte(line)}} {
		if err := encoder.Encode(item); err != nil {
			t.Fatal(err)
		}
	}
	// The aggregate stream exceeds the former 64 MiB limit, while each run is small.
	for i := 0; i < 70; i++ {
		data := []byte(line + strings.Repeat(" ", 1<<20))
		if err := encoder.Encode(pulledRun{Name: fmt.Sprintf("readonly-collection-%03d", i), Data: data}); err != nil {
			t.Fatal(err)
		}
	}
	var out bytes.Buffer
	err := processPulled(&stream, dest, &out)
	if err == nil || !strings.Contains(err.Error(), "oversize") || !strings.Contains(err.Error(), "conflict") {
		t.Fatalf("missing batch errors: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "readonly-collection-069.jsonl")); err != nil {
		t.Fatal(err)
	}
}

func TestProcessPulledCollections(t *testing.T) {
	dest := t.TempDir()
	line := `{"trust":"unverified_candidate_not_for_actions","reference":"frame","captured_at":"2026-01-01T00:00:00Z","scalars":{"score":0},"unknown":[]}` + "\n"
	encode := func(items ...pulledRun) string {
		var b bytes.Buffer
		for _, item := range items {
			if err := json.NewEncoder(&b).Encode(item); err != nil {
				t.Fatal(err)
			}
		}
		return b.String()
	}
	first := encode(pulledRun{Name: "readonly-collection-1", Data: []byte(line + "{")})
	var out bytes.Buffer
	if err := processPulled(strings.NewReader(first), dest, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), candidateTrust) {
		t.Fatal(out.String())
	}
	path := filepath.Join(dest, "readonly-collection-1.jsonl")
	got, err := os.ReadFile(path)
	if err != nil || string(got) != line {
		t.Fatalf("%s %v", got, err)
	}
	next := encode(pulledRun{Name: "readonly-collection-1", Data: []byte(line + line)})
	if err := processPulled(strings.NewReader(next), dest, &out); err != nil {
		t.Fatal(err)
	}
	if err := processPulled(strings.NewReader(next), dest, &out); err != nil {
		t.Fatal(err)
	}
	if err := processPulled(strings.NewReader(first), dest, &out); err == nil {
		t.Fatal("accepted truncated source")
	}
	for _, bad := range []string{`{`, encode(pulledRun{Name: "readonly-collection-../bad", Data: []byte(line)}), encode(pulledRun{Name: "readonly-collection-1", Data: []byte(line)}, pulledRun{Name: "readonly-collection-1", Data: []byte(line)})} {
		if err := processPulled(strings.NewReader(bad), dest, &out); err == nil {
			t.Fatal("accepted invalid response")
		}
	}
	got, err = os.ReadFile(path)
	if err != nil || string(got) != line+line {
		t.Fatal("lost imported records")
	}
}
