// Package matchlog writes local, append-only match events for offline analysis.
package matchlog

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type Event struct {
	SchemaVersion      int                `json:"schema_version"`
	MatchID            string             `json:"match_id"`
	Sequence           uint64             `json:"sequence"`
	UTC                string             `json:"utc"`
	ElapsedNS          int64              `json:"elapsed_ns"`
	EventType          string             `json:"event_type"`
	StateVersion       *uint64            `json:"state_version"`
	BuildSHA256        *string            `json:"build_sha256"`
	ConfigSHA256       *string            `json:"config_sha256"`
	RecognizerSHA256   *string            `json:"recognizer_sha256"`
	PolicySHA256       *string            `json:"policy_sha256"`
	Phase              *string            `json:"phase"`
	FramePath          *string            `json:"frame_path"`
	VisibleState       any                `json:"visible_state"`
	ActionIntent       any                `json:"action_intent"`
	EventsSent         *int               `json:"events_sent"`
	Confirmation       *string            `json:"confirmation"`
	TimingMS           map[string]float64 `json:"timing_ms"`
	Result             *string            `json:"result"`
	GG                 *bool              `json:"gg"`
	ManualIntervention *bool              `json:"manual_intervention"`
	Error              *string            `json:"error"`
}

type Writer struct {
	file    *os.File
	dir, id string
	started time.Time
	seq     uint64
	closed  bool
}

func Create(dir, id string) (*Writer, error) {
	if id == "" {
		return nil, fmt.Errorf("match ID required")
	}
	if err := os.Mkdir(dir, 0700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, "events.jsonl"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	return &Writer{file: f, dir: dir, id: id, started: time.Now()}, nil
}

func (w *Writer) Append(e Event) error {
	if w.closed {
		return fmt.Errorf("match log is closed")
	}
	if e.EventType == "" || e.EventType == "match_completed" || e.EventType == "match_incomplete" {
		return fmt.Errorf("invalid nonterminal event")
	}
	return w.append(e)
}
func (w *Writer) append(e Event) error {
	e.SchemaVersion = 1
	e.MatchID = w.id
	e.Sequence = w.seq + 1
	e.UTC = time.Now().UTC().Format(time.RFC3339Nano)
	e.ElapsedNS = time.Since(w.started).Nanoseconds()
	for _, v := range e.TimingMS {
		if v < 0 {
			return fmt.Errorf("negative timing")
		}
	}
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	n, err := w.file.Write(b)
	if err != nil || n != len(b) {
		w.closed = true
		_ = w.file.Close()
		return fmt.Errorf("event write incomplete: %d/%d: %v", n, len(b), err)
	}
	w.seq++
	return nil
}

// Abort closes an unfinished log without publishing a completion marker.
func (w *Writer) Abort() error {
	if w.closed {
		return nil
	}
	w.closed = true
	return w.file.Close()
}

// Finish writes a terminal record and publishes COMPLETE only after the file is closed.
func (w *Writer) Finish(e Event) error {
	if w.closed {
		return fmt.Errorf("match log is closed")
	}
	if e.EventType != "match_completed" && e.EventType != "match_incomplete" {
		return fmt.Errorf("terminal event required")
	}
	if e.Result != nil && *e.Result != "win" && *e.Result != "loss" && *e.Result != "draw" && *e.Result != "unknown" {
		return fmt.Errorf("invalid result")
	}
	if err := w.append(e); err != nil {
		return err
	}
	w.closed = true
	if err := w.file.Sync(); err != nil {
		_ = w.file.Close()
		return err
	}
	if err := w.file.Close(); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(w.dir, "COMPLETE"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	return f.Close()
}
