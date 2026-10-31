package matchlog

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestTerminalPublication(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "match")
	w, err := Create(dir, "m1")
	if err != nil {
		t.Fatal(err)
	}
	if err = w.Append(Event{EventType: "observation"}); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(dir, "COMPLETE")); !os.IsNotExist(err) {
		t.Fatal("premature marker")
	}
	if err = w.Finish(Event{EventType: "match_incomplete"}); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(filepath.Join(dir, "COMPLETE")); err != nil || info.Size() != 0 {
		t.Fatalf("marker: %v", err)
	}
	f, err := os.Open(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	i := uint64(0)
	for s.Scan() {
		i++
		var e Event
		if err = json.Unmarshal(s.Bytes(), &e); err != nil {
			t.Fatal(err)
		}
		if e.MatchID != "m1" || e.Sequence != i || e.SchemaVersion != 1 {
			t.Fatalf("bad event %+v", e)
		}
	}
	if err = s.Err(); err != nil {
		t.Fatal(err)
	}
	if i != 2 {
		t.Fatalf("got %d events", i)
	}
	if err = w.Append(Event{EventType: "observation"}); err == nil {
		t.Fatal("append accepted after finish")
	}
}
func TestAbortLeavesUnpublishedLog(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "match")
	w, err := Create(dir, "m")
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Append(Event{EventType: "observation"}); err != nil {
		t.Fatal(err)
	}
	if err := w.Abort(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "COMPLETE")); !os.IsNotExist(err) {
		t.Fatal("aborted log published")
	}
	if err := w.Append(Event{EventType: "observation"}); err == nil {
		t.Fatal("append accepted after abort")
	}
}

func TestInvalidTerminalNotPublished(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "match")
	w, err := Create(dir, "m")
	if err != nil {
		t.Fatal(err)
	}
	result := "invented"
	if err = w.Finish(Event{EventType: "match_completed", Result: &result}); err == nil {
		t.Fatal("invalid result accepted")
	}
	if _, err = os.Stat(filepath.Join(dir, "COMPLETE")); !os.IsNotExist(err) {
		t.Fatal("marker exists")
	}
	if err = w.Finish(Event{EventType: "match_incomplete"}); err != nil {
		t.Fatal(err)
	}
}
