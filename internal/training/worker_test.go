package training

import (
	"context"
	"encoding/json"

	"gwentcoachmvp/internal/strategy"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIngestionRestartAndRejection(t *testing.T) {
	root := t.TempDir()
	inbox := filepath.Join(root, "inbox")
	store := filepath.Join(root, "store")
	if err := os.Mkdir(inbox, 0700); err != nil {
		t.Fatal(err)
	}
	makeMatch := func(id, events string, complete bool, export bool) {
		t.Helper()
		d := filepath.Join(inbox, id)
		if err := os.Mkdir(d, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "events.jsonl"), []byte(events), 0600); err != nil {
			t.Fatal(err)
		}
		if complete {
			os.WriteFile(filepath.Join(d, "COMPLETE"), nil, 0600)
		}
		if export {
			os.WriteFile(filepath.Join(d, "training.jsonl"), nil, 0600)
		}
	}
	event := func(id, result string) string {
		return `{"schema_version":1,"match_id":"` + id + `","sequence":1,"utc":"2026-01-01T00:00:00Z","elapsed_ns":0,"event_type":"match_completed","result":"` + result + `"}` + "\n"
	}
	makeMatch("valid", event("valid", "unknown"), true, true)
	makeMatch("partial", event("partial", "win"), false, true)
	makeMatch("broken", strings.TrimSpace(event("broken", "win")), true, true)
	makeMatch("missing", event("missing", "win"), true, false)
	c := Config{inbox, store, "native-v1", "14.10.0", "offline-statistics-v1"}
	reports, err := Run(c)
	if err != nil {
		t.Fatal(err)
	}
	statuses := map[string]string{}
	for _, r := range reports {
		statuses[r.MatchID] = r.Status
	}
	for id, want := range map[string]string{"valid": "imported", "partial": "incomplete", "broken": "rejected", "missing": "rejected"} {
		if statuses[id] != want {
			t.Errorf("%s: %s", id, statuses[id])
		}
	}
	b, err := os.ReadFile(filepath.Join(store, "candidate.json"))
	if err != nil {
		t.Fatal(err)
	}
	var a struct {
		Payload []byte `json:"payload"`
		Digest  string `json:"digest"`
	}
	if err = json.Unmarshal(b, &a); err != nil {
		t.Fatal(err)
	}
	artifact, err := strategy.LoadArtifact(b, strategy.Compatibility{RulesVersion: c.RulesVersion, CatalogVersion: c.CatalogVersion})
	if err != nil {
		t.Fatal(err)
	}
	ranker, err := strategy.NewRanker(artifact.Payload)
	if err != nil {
		t.Fatal(err)
	}
	candidates := []string{`{"Kind":"pass"}`, `{"Kind":"play","Card":"a"}`}
	choice, err := ranker.Choose(context.Background(), strategy.Observation{Candidates: candidates})
	if err != nil || choice != candidates[0] {
		t.Fatalf("neutral ranker: %q %v", choice, err)
	}
	if !strings.Contains(string(a.Payload), `"kind":"action-ranker-v1"`) {
		t.Fatalf("unexpected payload: %s", a.Payload)
	}
	report, err := os.ReadFile(filepath.Join(store, "report.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(report), `"unknown": 1`) || strings.Contains(string(report), `"wins": 1`) {
		t.Fatalf("unexpected metrics: %s", report)
	}
	reports, err = Run(c)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range reports {
		if r.MatchID == "valid" && r.Status != "duplicate" {
			t.Fatalf("restart: %+v", r)
		}
	}
}

func TestMissingReceiptRejected(t *testing.T) {
	data := `{"schema_version":1,"match_id":"m","sequence":1,"utc":"2026-01-01T00:00:00Z","event_type":"action_intent","action_id":"a","action_domain":"gameplay","state_version":1}` + "\n" + `{"schema_version":1,"match_id":"m","sequence":2,"utc":"2026-01-01T00:00:01Z","event_type":"input_intent","action_id":"a","step":1,"state_version":1,"action_intent":{"kind":"click"}}` + "\n" + `{"schema_version":1,"match_id":"m","sequence":3,"utc":"2026-01-01T00:00:02Z","event_type":"match_completed","result":"win"}` + "\n"
	if _, err := validate("m", []byte(data)); err == nil {
		t.Fatal("accepted action without receipt")
	}
}
