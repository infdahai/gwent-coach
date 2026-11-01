// Package training processes sealed match logs independently of game execution.
package training

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gwentcoachmvp/internal/matchlog"
)

type Event = matchlog.Event
type Report struct {
	MatchID string `json:"match_id"`
	Status  string `json:"status"`
	Detail  string `json:"detail,omitempty"`
}
type Metrics struct {
	Matches       int                  `json:"matches"`
	Wins          int                  `json:"wins"`
	Losses        int                  `json:"losses"`
	Draws         int                  `json:"draws"`
	Unknown       int                  `json:"unknown"`
	Actions       int                  `json:"confirmed_actions"`
	Errors        int                  `json:"error_events"`
	Timeouts      int                  `json:"timeout_events"`
	Interventions int                  `json:"manual_interventions"`
	Timing        map[string][]float64 `json:"timing_ms"`
}
type Config struct{ Inbox, Store, RulesVersion, CatalogVersion, PolicyVersion string }
type item struct {
	id     string
	data   []byte
	events []Event
	traces []matchlog.ActionTrace
}

func validate(id string, data []byte) (item, error) {
	if len(data) == 0 || data[len(data)-1] != '\n' {
		return item{}, fmt.Errorf("empty or partial event stream")
	}
	s := bufio.NewScanner(bytes.NewReader(data))
	s.Buffer(make([]byte, 4096), 16<<20)
	var events []Event
	var last int64
	var session string
	for s.Scan() {
		var e Event
		if err := json.Unmarshal(s.Bytes(), &e); err != nil {
			return item{}, err
		}
		if e.SchemaVersion != 1 || e.MatchID != id || e.Sequence != uint64(len(events)+1) || e.EventType == "" || e.ElapsedNS < 0 || (len(events) > 0 && e.ElapsedNS < last) {
			return item{}, fmt.Errorf("invalid event identity, order or timing")
		}
		if e.SessionID != "" {
			if session != "" && session != e.SessionID {
				return item{}, fmt.Errorf("conflicting session IDs")
			}
			session = e.SessionID
		}
		if _, err := time.Parse(time.RFC3339Nano, e.UTC); err != nil {
			return item{}, err
		}
		for _, v := range e.TimingMS {
			if v < 0 {
				return item{}, fmt.Errorf("negative timing")
			}
		}
		if len(events) > 0 && (events[len(events)-1].EventType == "match_completed" || events[len(events)-1].EventType == "match_incomplete") {
			return item{}, fmt.Errorf("event after terminal")
		}
		last = e.ElapsedNS
		events = append(events, e)
	}
	if err := s.Err(); err != nil {
		return item{}, err
	}
	if len(events) == 0 || events[len(events)-1].EventType != "match_completed" {
		return item{}, fmt.Errorf("completed match required")
	}
	r := events[len(events)-1].Result
	if r != nil && *r != "win" && *r != "loss" && *r != "draw" && *r != "unknown" {
		return item{}, fmt.Errorf("invalid result")
	}
	traces, err := matchlog.ReplayActions(events)
	if err != nil {
		return item{}, err
	}
	for _, t := range traces {
		if t.Confirmed == nil {
			return item{}, fmt.Errorf("unconfirmed action or missing receipt")
		}
	}
	return item{id: id, data: data, events: events, traces: traces}, nil
}

// atomicWrite uses a temporary file and rename; callers own the destination directory.
func atomicWrite(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".pending-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	if e := f.Close(); err == nil {
		err = e
	}
	if err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
func digest(data []byte) string {
	s := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(s[:])
}

func Run(c Config) ([]Report, error) {
	if c.Inbox == "" || c.Store == "" || c.RulesVersion == "" || c.CatalogVersion == "" || c.PolicyVersion == "" {
		return nil, fmt.Errorf("inbox, store and all versions are required")
	}
	if err := os.MkdirAll(filepath.Join(c.Store, "matches"), 0700); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(c.Inbox)
	if err != nil {
		return nil, err
	}
	var reports []Report
	for _, ent := range entries {
		if !ent.IsDir() {
			continue
		}
		id := ent.Name()
		dir := filepath.Join(c.Inbox, id)
		marker, err := os.ReadFile(filepath.Join(dir, "COMPLETE"))
		if os.IsNotExist(err) {
			reports = append(reports, Report{id, "incomplete", "missing COMPLETE"})
			continue
		}
		if err != nil {
			return reports, err
		}
		if len(marker) != 0 {
			reports = append(reports, Report{id, "rejected", "invalid COMPLETE marker"})
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, "events.jsonl"))
		if err != nil {
			reports = append(reports, Report{id, "rejected", err.Error()})
			continue
		}
		if len(data) > 16<<20 {
			reports = append(reports, Report{id, "rejected", "event stream exceeds 16 MiB"})
			continue
		}
		match, err := validate(id, data)
		if err != nil {
			reports = append(reports, Report{id, "rejected", err.Error()})
			continue
		}
		// The export must match the exact confirmed gameplay samples reconstructed from the event stream.
		export, err := os.ReadFile(filepath.Join(dir, "training.jsonl"))
		if err != nil {
			reports = append(reports, Report{id, "rejected", "missing training export"})
			continue
		}
		if err = validateExport(export, match); err != nil {
			reports = append(reports, Report{id, "rejected", err.Error()})
			continue
		}
		dest := filepath.Join(c.Store, "matches", id+".jsonl")
		old, err := os.ReadFile(dest)
		if err == nil {
			if !bytes.Equal(old, data) {
				reports = append(reports, Report{id, "rejected", "match ID conflict"})
			} else {
				reports = append(reports, Report{id, "duplicate", ""})
			}
			continue
		}
		if !os.IsNotExist(err) {
			return reports, err
		}
		if err = atomicWrite(dest, data); err != nil {
			return reports, err
		}
		reports = append(reports, Report{id, "imported", digest(data)})
	}
	if err = compute(c); err != nil {
		return reports, err
	}
	return reports, nil
}

func validateExport(data []byte, m item) error {
	var expected []struct{ Intent, Confirmation uint64 }
	for _, t := range m.traces {
		if t.Intent.ActionDomain == "gameplay" && t.Confirmed != nil {
			expected = append(expected, struct{ Intent, Confirmation uint64 }{t.Intent.Sequence, t.Confirmed.Sequence})
		}
	}
	s := bufio.NewScanner(bytes.NewReader(data))
	s.Buffer(make([]byte, 4096), 16<<20)
	n := 0
	for s.Scan() {
		var sample struct {
			Intent       json.RawMessage `json:"intent"`
			Confirmation json.RawMessage `json:"confirmation"`
		}
		if err := json.Unmarshal(s.Bytes(), &sample); err != nil {
			return fmt.Errorf("invalid training export: %w", err)
		}
		if n >= len(expected) {
			return fmt.Errorf("unexpected training sample")
		}
		for _, pair := range []struct {
			raw json.RawMessage
			seq uint64
		}{{sample.Intent, expected[n].Intent}, {sample.Confirmation, expected[n].Confirmation}} {
			var v any
			if err := json.Unmarshal(pair.raw, &v); err != nil {
				return fmt.Errorf("invalid training sample")
			}
			var original any
			lines := bytes.Split(m.data, []byte{'\n'})
			if err := json.Unmarshal(lines[pair.seq-1], &original); err != nil {
				return err
			}
			a, _ := json.Marshal(v)
			b, _ := json.Marshal(original)
			if !bytes.Equal(a, b) {
				return fmt.Errorf("training sample differs from event stream")
			}
		}
		n++
	}
	if err := s.Err(); err != nil {
		return err
	}
	if n != len(expected) {
		return fmt.Errorf("missing training sample")
	}
	return nil
}

func compute(c Config) error {
	entries, err := os.ReadDir(filepath.Join(c.Store, "matches"))
	if err != nil {
		return err
	}
	var items []item
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".jsonl")
		b, err := os.ReadFile(filepath.Join(c.Store, "matches", e.Name()))
		if err != nil {
			return err
		}
		m, err := validate(id, b)
		if err != nil {
			return fmt.Errorf("stored match %s: %w", id, err)
		}
		items = append(items, m)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].id < items[j].id })
	// Legacy logs without session provenance remain descriptive only.
	train := Metrics{Timing: map[string][]float64{}}
	eval := Metrics{Timing: map[string][]float64{}}
	legacy := Metrics{Timing: map[string][]float64{}}
	var fingerprints []string
	actionCounts := map[string]int{}
	for _, m := range items {
		fingerprints = append(fingerprints, m.id+":"+digest(m.data))
		// Session IDs do not establish policy, rules or catalog provenance.
		target := &legacy
		target.Matches++
		// No independently validated result evidence contract exists yet.
		target.Unknown++
		for _, t := range m.traces {
			if t.Intent.ActionDomain == "gameplay" && t.Confirmed != nil {
				target.Actions++
				if target == &train {
					b, err := json.Marshal(t.Intent.ActionIntent)
					if err != nil {
						return err
					}
					actionCounts[string(b)]++
				}
			}
		}
		for _, e := range m.events {
			if e.Error != nil {
				target.Errors++
				if strings.Contains(strings.ToLower(*e.Error), "timeout") {
					target.Timeouts++
				}
			}
			if e.ManualIntervention != nil && *e.ManualIntervention {
				target.Interventions++
			}
			for k, v := range e.TimingMS {
				target.Timing[k] = append(target.Timing[k], v)
			}
		}
	}
	candidate := filepath.Join(c.Store, "candidate.json")
	if _, err := os.Stat(candidate); err == nil {
		f, err := os.CreateTemp(c.Store, "quarantined-candidate-*.json")
		if err != nil {
			return err
		}
		name := f.Name()
		if err = f.Close(); err != nil {
			return err
		}
		if err = os.Rename(candidate, name); err != nil {
			os.Remove(name)
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	report, err := json.MarshalIndent(struct {
		Kind                 string `json:"kind"`
		DataFingerprint      string `json:"data_fingerprint"`
		Split                string `json:"split"`
		Training, Evaluation Metrics
		Quarantined          Metrics        `json:"quarantined_unverified_provenance"`
		CandidateStatus      string         `json:"candidate_status"`
		Agreement            any            `json:"heldout_baseline_candidate_agreement"`
		AgreementReason      string         `json:"agreement_reason"`
		ActionCounts         map[string]int `json:"training_action_counts"`
		Limitation           string         `json:"limitation"`
	}{"offline-statistics-v1", digest([]byte(strings.Join(fingerprints, "\n"))), "no eligible sessions: policy, rules and catalog provenance unverified", train, eval, legacy, "insufficient_data", nil, "recorded logs do not contain ordered legal candidate sets", actionCounts, "outcomes unverified; no candidate or promotion"}, "", "  ")
	if err != nil {
		return err
	}
	if err = atomicWrite(filepath.Join(c.Store, "report.json"), append(report, '\n')); err != nil {
		return err
	}
	return nil
}
