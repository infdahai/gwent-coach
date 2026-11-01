package matchlog

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ExportConfirmedTraining atomically writes a derived per-match dataset. A
// matching existing file is accepted; differing data is never overwritten.
func ExportConfirmedTraining(path string, events []Event) error {
	if len(events) == 0 || events[len(events)-1].EventType != "match_completed" {
		return fmt.Errorf("completed match required for training export")
	}
	samples, err := ConfirmedTrainingEvents(events)
	if err != nil {
		return err
	}
	var data bytes.Buffer
	encoder := json.NewEncoder(&data)
	for _, sample := range samples {
		if err = encoder.Encode(sample); err != nil {
			return err
		}
	}
	if previous, e := os.ReadFile(path); e == nil {
		if bytes.Equal(previous, data.Bytes()) {
			return nil
		}
		return fmt.Errorf("existing training data differs from confirmed events")
	} else if !os.IsNotExist(e) {
		return e
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".training-")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err = file.Write(data.Bytes()); err != nil {
		file.Close()
		return err
	}
	if err = file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

// CompleteTraining can be repeated after a crash following the terminal log,
// export or marker. It performs no game input and never reopens terminal logs.
func CompleteTraining(dir, id string) error {
	if id == "" || id == "." || id == ".." || strings.ContainsAny(id, `/\`) || filepath.Base(dir) != id {
		return fmt.Errorf("training directory must equal safe match ID")
	}
	events, err := Read(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		return err
	}
	for _, e := range events {
		if e.MatchID != id {
			return fmt.Errorf("completion match ID mismatch")
		}
	}
	if err = ExportConfirmedTraining(filepath.Join(dir, "training.jsonl"), events); err != nil {
		return err
	}
	if _, err = os.Stat(filepath.Join(dir, "COMPLETE")); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	return publishComplete(dir)
}

// TrainingSample is joined only from a persisted intent, completed delivery and
// semantic confirmation. Receipt-only and navigation events produce no sample.
type TrainingSample struct {
	Intent       Event `json:"intent"`
	Confirmation Event `json:"confirmation"`
}

func ConfirmedTrainingEvents(events []Event) ([]TrainingSample, error) {
	traces, err := ReplayActions(events)
	if err != nil {
		return nil, err
	}
	var out []TrainingSample
	for _, trace := range traces {
		if trace.Intent.ActionDomain == "gameplay" && trace.Confirmed != nil {
			out = append(out, TrainingSample{Intent: trace.Intent, Confirmation: *trace.Confirmed})
		}
	}
	return out, nil
}
