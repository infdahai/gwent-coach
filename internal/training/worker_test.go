package training

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSemanticActionJSONUnwrapsPlan(t *testing.T) {
	b, err := semanticActionJSON(map[string]any{"Action": map[string]any{"Kind": "play", "Card": "wolf"}, "Inputs": []any{}})
	if err != nil || string(b) != `{"Card":"wolf","Kind":"play"}` {
		t.Fatalf("action=%s err=%v", b, err)
	}
}

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
	makeMatch("valid", strings.Replace(event("valid", "win"), `"sequence":1`, `"session_id":"s1","sequence":1`, 1), true, true)
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
	if _, err := os.Stat(filepath.Join(store, "candidate.json")); !os.IsNotExist(err) {
		t.Fatalf("unexpected candidate: %v", err)
	}
	report, err := os.ReadFile(filepath.Join(store, "report.json"))
	if err != nil {
		t.Fatal(err)
	}
	var metrics struct {
		Quarantined Metrics `json:"quarantined_unverified_provenance"`
		Training    Metrics
		Inventory   []struct {
			MatchID        string `json:"match_id"`
			EventDigest    string `json:"event_digest"`
			ClaimedSession string `json:"claimed_session_id"`
			ClaimedResult  string `json:"claimed_result"`
		} `json:"quarantine_inventory_unverified_claims"`
	}
	if err := json.Unmarshal(report, &metrics); err != nil {
		t.Fatal(err)
	}
	if metrics.Quarantined.Unknown != 1 || metrics.Quarantined.Wins != 0 || metrics.Training.Matches != 0 {
		t.Fatalf("unverified data counted: %s", report)
	}
	stored, err := os.ReadFile(filepath.Join(store, "matches", "valid.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if len(metrics.Inventory) != 1 || metrics.Inventory[0].MatchID != "valid" || metrics.Inventory[0].EventDigest != digest(stored) || metrics.Inventory[0].ClaimedSession != "s1" || metrics.Inventory[0].ClaimedResult != "win" {
		t.Fatalf("unexpected quarantine inventory: %+v", metrics.Inventory)
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
	restartedReport, err := os.ReadFile(filepath.Join(store, "report.json"))
	if err != nil || string(restartedReport) != string(report) {
		t.Fatalf("report changed on restart: %v", err)
	}
}

func TestMissingReceiptRejected(t *testing.T) {
	data := `{"schema_version":1,"match_id":"m","sequence":1,"utc":"2026-01-01T00:00:00Z","event_type":"action_intent","action_id":"a","action_domain":"gameplay","state_version":1}` + "\n" + `{"schema_version":1,"match_id":"m","sequence":2,"utc":"2026-01-01T00:00:01Z","event_type":"input_intent","action_id":"a","step":1,"state_version":1,"action_intent":{"kind":"click"}}` + "\n" + `{"schema_version":1,"match_id":"m","sequence":3,"utc":"2026-01-01T00:00:02Z","event_type":"match_completed","result":"win"}` + "\n"
	if _, err := validate("m", []byte(data)); err == nil {
		t.Fatal("accepted action without receipt")
	}
}
