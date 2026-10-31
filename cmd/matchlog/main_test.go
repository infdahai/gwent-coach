package main

import (
	"bytes"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
