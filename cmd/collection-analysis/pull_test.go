package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
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
	first := encode(pulledRun{"readonly-collection-1", []byte(line + "{")})
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
	next := encode(pulledRun{"readonly-collection-1", []byte(line + line)})
	if err := processPulled(strings.NewReader(next), dest, &out); err != nil {
		t.Fatal(err)
	}
	if err := processPulled(strings.NewReader(next), dest, &out); err != nil {
		t.Fatal(err)
	}
	if err := processPulled(strings.NewReader(first), dest, &out); err == nil {
		t.Fatal("accepted truncated source")
	}
	for _, bad := range []string{`{`, encode(pulledRun{"readonly-collection-../bad", []byte(line)}), encode(pulledRun{"readonly-collection-1", []byte(line)}, pulledRun{"readonly-collection-1", []byte(line)})} {
		if err := processPulled(strings.NewReader(bad), dest, &out); err == nil {
			t.Fatal("accepted invalid response")
		}
	}
	got, err = os.ReadFile(path)
	if err != nil || string(got) != line+line {
		t.Fatal("lost imported records")
	}
}
