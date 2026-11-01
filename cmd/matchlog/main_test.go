package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const valid = `{"schema_version":1,"match_id":"m1","sequence":1,"utc":"2026-10-31T12:00:00Z","elapsed_ns":0,"event_type":"observation","timing_ms":{"observation":12}}
{"schema_version":1,"match_id":"m1","sequence":2,"utc":"2026-10-31T12:00:01Z","elapsed_ns":100,"event_type":"match_completed","result":null}
`

func TestParse(t *testing.T) {
	r, err := parse([]byte(valid))
	if err != nil || r.Outcome != "unknown" || r.Events != 2 || len(r.Timings["observation"]) != 1 {
		t.Fatalf("%+v %v", r, err)
	}
	cases := map[string]string{"partial": strings.TrimSuffix(valid, "\n"), "malformed": "{\n", "sequence": strings.Replace(valid, `"sequence":2`, `"sequence":3`, 1), "time": strings.Replace(valid, `"elapsed_ns":100`, `"elapsed_ns":-1`, 1), "missing terminal": strings.SplitAfter(valid, "\n")[0], "after terminal": valid + strings.SplitAfter(valid, "\n")[0], "bad duration": strings.Replace(valid, `"observation":12`, `"observation":-1`, 1)}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := parse([]byte(data)); err == nil {
				t.Fatal("expected error")
			}
		})
	}
	r, err = parse([]byte(strings.Replace(valid, `"event_type":"match_completed"`, `"event_type":"match_incomplete"`, 1)))
	if err != nil || r.Status != "match_incomplete" {
		t.Fatalf("%+v %v", r, err)
	}
}
func TestErrorCounts(t *testing.T) {
	data := strings.Replace(valid, `"event_type":"observation"`, `"error":"\u0074imeout","event_type":"observation"`, 1)
	data = strings.Replace(data, `"event_type":"match_completed"`, `"error":"","event_type":"match_completed"`, 1)
	r, err := parse([]byte(data))
	if err != nil || r.Failures != 2 || r.Timeouts != 1 {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestFetchStreamsMockSSH(t *testing.T) {
	dir := t.TempDir()
	script := "#!/bin/sh\nprintf '%s\\n' '" + fmt.Sprintf(`{"name":"m1","complete":true,"data":"%s","error":""}`, base64.StdEncoding.EncodeToString([]byte(valid))) + "'\n"
	if err := os.WriteFile(filepath.Join(dir, "ssh"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	var items []remoteItem
	if err := fetch(context.Background(), "test", `C:\test`, time.Second, func(item remoteItem) error { items = append(items, item); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Name != "m1" {
		t.Fatalf("%+v", items)
	}
}

func TestFetchStalledSSH(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ssh"), []byte("#!/bin/sh\nexec sleep 30\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	start := time.Now()
	err := fetch(context.Background(), "test", `C:\test`, 100*time.Millisecond, func(remoteItem) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "no output progress") || time.Since(start) > 3*time.Second {
		t.Fatalf("timeout: %v after %s", err, time.Since(start))
	}
}

func TestImportSealedTraining(t *testing.T) {
	root := t.TempDir()
	empty := ""
	item := remoteItem{Name: "m1", Complete: true, Data: base64.StdEncoding.EncodeToString([]byte(valid)), Training: &empty}
	var out bytes.Buffer
	if err := importItems(root, []remoteItem{item, item}, &out); err != nil {
		t.Fatal(err)
	}
	for name, expected := range map[string]string{"events.jsonl": valid, "training.jsonl": "", "COMPLETE": ""} {
		got, err := os.ReadFile(filepath.Join(root, "m1", name))
		if err != nil || string(got) != expected {
			t.Fatalf("%s: %q %v", name, got, err)
		}
	}
	bad := base64.StdEncoding.EncodeToString([]byte("{}\n"))
	item.Training = &bad
	if err := importItems(root, []remoteItem{item}, &out); err == nil {
		t.Fatal("mismatched training export accepted")
	}
}

func TestImportAndDuplicates(t *testing.T) {
	dir := t.TempDir()
	item := remoteItem{Name: "m1", Complete: true, Data: base64.StdEncoding.EncodeToString([]byte(valid))}
	var out bytes.Buffer
	if err := importItems(dir, []remoteItem{{Name: "unfinished"}, item}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "unpublished") {
		t.Fatal(out.String())
	}
	if err := importItems(dir, []remoteItem{item}, &out); err != nil {
		t.Fatal(err)
	}
	changed := strings.Replace(valid, `"observation":12`, `"observation":13`, 1)
	item.Data = base64.StdEncoding.EncodeToString([]byte(changed))
	if err := importItems(dir, []remoteItem{item}, &out); err == nil {
		t.Fatal("expected conflict")
	}
	p := filepath.Join(dir, "m1.jsonl")
	if err := analyze([]string{p, p}, &out); err == nil {
		t.Fatal("expected duplicate error")
	}
	if err := os.WriteFile(filepath.Join(dir, "empty"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := analyze([]string{filepath.Join(dir, "empty")}, &out); err == nil {
		t.Fatal("expected empty error")
	}
}
