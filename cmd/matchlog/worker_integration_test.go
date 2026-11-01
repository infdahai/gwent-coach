package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gwentcoachmvp/internal/matchlog"
	"gwentcoachmvp/internal/observe"
	"gwentcoachmvp/internal/stateestimate"
	"gwentcoachmvp/internal/training"
)

func TestProducedResultClaimRemainsQuarantined(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "m")
	w, err := matchlog.Create(dir, "m")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	o := observe.SemanticObservation{MatchID: "m", Source: "second", Phase: "result", CalibrationID: "cal", Player: 0, CapturedAt: now, Result: true, PartialResult: &stateestimate.OutcomeFacts{Finished: true, Winner: 1}, Frames: []matchlog.FrameEvidence{{Source: "first", MatchID: "m", CapturedAt: now.Add(-time.Millisecond)}, {Source: "second", MatchID: "m", CapturedAt: now}}}
	if err := observe.NewActionJournal(w).Result(observe.Snapshot{Version: 1, Source: o.Source, Phase: o.Phase}, o); err != nil {
		t.Fatal(err)
	}
	loss := "loss"
	if err := w.Seal(matchlog.Event{EventType: "match_completed", Result: &loss}); err != nil {
		t.Fatal(err)
	}
	if err := matchlog.CompleteTraining(dir, "m"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	export, err := os.ReadFile(filepath.Join(dir, "training.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	encoded := base64.StdEncoding.EncodeToString(export)
	item := remoteItem{Name: "m", Complete: true, Data: base64.StdEncoding.EncodeToString(data), Training: &encoded}
	inbox, store := filepath.Join(root, "inbox"), filepath.Join(root, "store")
	if err := importItems(inbox, []remoteItem{item}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	cfg := training.Config{Inbox: inbox, Store: store, RulesVersion: "r", CatalogVersion: "c", PolicyVersion: "p"}
	reports, err := training.Run(cfg)
	if err != nil || len(reports) != 1 || reports[0].Status != "imported" {
		t.Fatalf("reports=%+v err=%v", reports, err)
	}
	b, err := os.ReadFile(filepath.Join(store, "report.json"))
	if err != nil {
		t.Fatal(err)
	}
	var report struct {
		Quarantined training.Metrics `json:"quarantined_unverified_provenance"`
		Training    training.Metrics
	}
	if err := json.Unmarshal(b, &report); err != nil {
		t.Fatal(err)
	}
	if report.Quarantined.Unknown != 1 || report.Quarantined.Losses != 0 || report.Training.Matches != 0 {
		t.Fatalf("unverified result trusted: %s", b)
	}
	for name, altered := range map[string]string{
		"winner":   strings.Replace(string(data), `"Winner":1`, `"Winner":0`, 1),
		"player":   strings.Replace(string(data), `"result_player":0`, `"result_player":1`, 1),
		"frame":    strings.Replace(string(data), `"source":"first"`, `"source":"second"`, 1),
		"terminal": strings.Replace(string(data), `"result":"loss"`, `"result":"win"`, 2),
	} {
		var events []matchlog.Event
		for _, line := range bytes.Split(bytes.TrimSpace([]byte(altered)), []byte{'\n'}) {
			var event matchlog.Event
			if err := json.Unmarshal(line, &event); err != nil {
				t.Fatal(err)
			}
			events = append(events, event)
		}
		if err := matchlog.ValidateResultClaims(events); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	bad := strings.Replace(string(data), `"result":"loss"`, `"result":"win"`, 1)
	item.Data = base64.StdEncoding.EncodeToString([]byte(bad))
	if err := importItems(filepath.Join(root, "bad-inbox"), []remoteItem{item}, &bytes.Buffer{}); err == nil {
		t.Fatal("import accepted inconsistent claim")
	}
	if err := os.WriteFile(filepath.Join(inbox, "m", "events.jsonl"), []byte(bad), 0600); err != nil {
		t.Fatal(err)
	}
	reports, err = training.Run(cfg)
	if err != nil || len(reports) != 1 || reports[0].Status != "rejected" {
		t.Fatalf("tampered reports=%+v err=%v", reports, err)
	}
}

func TestSealedImportFeedsWorker(t *testing.T) {
	root := t.TempDir()
	inbox := filepath.Join(root, "inbox")
	store := filepath.Join(root, "store")
	empty := ""
	item := remoteItem{Name: "m1", Complete: true, Data: base64.StdEncoding.EncodeToString([]byte(valid)), Training: &empty}
	if err := importItems(inbox, []remoteItem{item}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	c := training.Config{Inbox: inbox, Store: store, RulesVersion: "native-v1", CatalogVersion: "14.10.0", PolicyVersion: "action-ranker-v1"}
	for _, want := range []string{"imported", "duplicate"} {
		reports, err := training.Run(c)
		if err != nil {
			t.Fatal(err)
		}
		if len(reports) != 1 || reports[0].Status != want {
			t.Fatalf("want %s: %+v", want, reports)
		}
	}
}
