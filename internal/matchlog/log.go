// Package matchlog writes local, append-only match events for offline analysis.
package matchlog

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type FrameEvidence struct {
	Source     string    `json:"source"`
	MatchID    string    `json:"match_id"`
	CapturedAt time.Time `json:"captured_at"`
}

type Event struct {
	SchemaVersion      int                `json:"schema_version"`
	MatchID            string             `json:"match_id"`
	SessionID          string             `json:"session_id,omitempty"`
	Sequence           uint64             `json:"sequence"`
	UTC                string             `json:"utc"`
	ElapsedNS          int64              `json:"elapsed_ns"`
	EventType          string             `json:"event_type"`
	ActionID           string             `json:"action_id,omitempty"`
	ActionDomain       string             `json:"action_domain,omitempty"`
	Step               *uint64            `json:"step,omitempty"`
	DeliveredAt        *string            `json:"delivered_at,omitempty"`
	DeliveryStatus     string             `json:"delivery_status,omitempty"`
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
	ConfirmationFrames []FrameEvidence    `json:"confirmation_frames,omitempty"`
	CapturedAt         *string            `json:"captured_at,omitempty"`
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

// Read returns a complete append-only stream. A torn final record is unresolved;
// recovery never truncates it or assumes its input was not sent.
func Read(path string) ([]Event, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(data) > 0 && data[len(data)-1] != '\n' {
		return nil, fmt.Errorf("incomplete final event; manual reconciliation required")
	}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 4096), 16<<20)
	var events []Event
	for scanner.Scan() {
		var e Event
		if err := json.Unmarshal(scanner.Bytes(), &e); err != nil {
			return nil, err
		}
		if e.SchemaVersion != 1 || e.Sequence != uint64(len(events)+1) || e.MatchID == "" || (len(events) > 0 && e.MatchID != events[0].MatchID) {
			return nil, fmt.Errorf("invalid event stream")
		}
		events = append(events, e)
	}
	return events, scanner.Err()
}

// Resume opens an unfinished stream after the caller has acquired exclusive
// session ownership. It retains event sequence independently of semantic state.
func Resume(dir, id string) (*Writer, []Event, error) {
	if _, err := os.Stat(filepath.Join(dir, "COMPLETE")); err == nil {
		return nil, nil, fmt.Errorf("completed log cannot resume")
	} else if !os.IsNotExist(err) {
		return nil, nil, err
	}
	events, err := Read(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		return nil, nil, err
	}
	if id == "" {
		return nil, nil, fmt.Errorf("match ID required")
	}
	started := time.Now()
	var seq uint64
	for _, e := range events {
		if e.MatchID != id || e.EventType == "match_completed" || e.EventType == "match_incomplete" {
			return nil, nil, fmt.Errorf("terminal or mismatched log")
		}
		seq = e.Sequence
	}
	if len(events) > 0 {
		stamp, e := time.Parse(time.RFC3339Nano, events[len(events)-1].UTC)
		if e != nil {
			return nil, nil, e
		}
		started = stamp.Add(-time.Duration(events[len(events)-1].ElapsedNS))
	}
	f, err := os.OpenFile(filepath.Join(dir, "events.jsonl"), os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return nil, nil, err
	}
	return &Writer{file: f, dir: dir, id: id, started: started, seq: seq}, events, nil
}

func (w *Writer) MatchID() string { return w.id }

func (w *Writer) Append(e Event) error {
	if w.closed {
		return fmt.Errorf("match log is closed")
	}
	if e.EventType == "" || e.EventType == "match_completed" || e.EventType == "match_incomplete" {
		return fmt.Errorf("invalid nonterminal event")
	}
	return w.append(e)
}

// AppendDurable persists a boundary before the caller sends or confirms input.
// Writers are owned by the serialized session controller.
func (w *Writer) AppendDurable(e Event) error {
	if err := w.Append(e); err != nil {
		return err
	}
	if err := w.file.Sync(); err != nil {
		w.closed = true
		_ = w.file.Close()
		return fmt.Errorf("sync match event: %w", err)
	}
	return nil
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

// Finish preserves the generic log API. Training sessions use Seal followed by
// CompleteTraining so their dataset is durable before publishing COMPLETE.
func (w *Writer) Finish(e Event) error {
	if err := w.Seal(e); err != nil {
		return err
	}
	return publishComplete(w.dir)
}

// Seal durably closes a terminal log without publishing a completion marker.
func (w *Writer) Seal(e Event) error {
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
	return w.file.Close()
}

func publishComplete(dir string) error {
	f, err := os.OpenFile(filepath.Join(dir, "COMPLETE"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	return f.Close()
}
