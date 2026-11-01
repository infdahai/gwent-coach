package observe

import (
	"encoding/json"
	"fmt"
	"image"
	"io"
	"time"
)

// NativeSliceConfig binds the one observed opening to independently calibrated
// phase labels and native coordinates. It is not a general card-playing policy.
type NativeSliceConfig struct {
	ModelPath, ModelSHA256, StopFile, StateDir string
	Card, Row, EndTurn                         image.Point
}

type nativeSliceEvent struct {
	Stage     string  `json:"stage"`
	ElapsedMS float64 `json:"elapsed_ms"`
	Reference string  `json:"reference,omitempty"`
}

// runNativeSlice runs at most one opening: select the known card, place it in
// the back row, then end the turn. Every delivered input is followed by an
// independently captured phase; uncertainty terminates without retry.
func runNativeSlice(cfg NativeSliceConfig, capture func() (navFrame, error), send func(navFrame, image.Point, time.Time) error, pause func() error, wait func(), output io.Writer) error {
	for _, p := range []image.Point{cfg.Card, cfg.Row, cfg.EndTurn} {
		if p == (image.Point{}) || !p.In(image.Rect(0, 0, 1920, 1080)) {
			return fmt.Errorf("all three calibrated points must be inside the client")
		}
	}
	started := time.Time{}
	deadline := time.Time{}
	record := func(stage string, frame navFrame) error {
		elapsed := 0.0
		if !started.IsZero() {
			elapsed = float64(time.Since(started)) / float64(time.Millisecond)
		}
		return json.NewEncoder(output).Encode(nativeSliceEvent{Stage: stage, ElapsedMS: elapsed, Reference: frame.Reference})
	}
	get := func() (navFrame, error) {
		if err := pause(); err != nil {
			return navFrame{}, err
		}
		frame, err := capture()
		if err != nil {
			return frame, err
		}
		if age := time.Since(frame.Captured); frame.Reference == "" || frame.Width != 1920 || frame.Height != 1080 || frame.Captured.IsZero() || age < 0 || age > 2*time.Second {
			return frame, fmt.Errorf("stale or invalid native observation")
		}
		return frame, nil
	}
	var ready navFrame
	for {
		frame, err := get()
		if err != nil {
			return err
		}
		if frame.Phase == "slice-ready" {
			if ready.Reference == "" {
				started = time.Now()
				deadline = started.Add(10 * time.Second)
			} else if !time.Now().Before(deadline) {
				return fmt.Errorf("10-second deadline expired while confirming ready state")
			} else if ready.Reference != frame.Reference {
				ready = frame
				break
			}
			ready = frame
		} else {
			ready = navFrame{}
			started, deadline = time.Time{}, time.Time{}
		}
		wait()
	}
	if err := record("ready", ready); err != nil {
		return err
	}
	stages := []struct {
		name, phase string
		point       image.Point
	}{
		{"select", "slice-selected", cfg.Card},
		{"place", "slice-placed", cfg.Row},
		{"end_turn", "slice-ended", cfg.EndTurn},
	}
	frame := ready
	for _, stage := range stages {
		if !stage.point.In(image.Rect(0, 0, 1920, 1080)) {
			return fmt.Errorf("%s point outside client", stage.name)
		}
		if err := pause(); err != nil {
			return err
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf("10-second deadline expired before %s", stage.name)
		}
		if err := record(stage.name+"_intent", frame); err != nil {
			return err
		}
		if err := send(frame, stage.point, deadline); err != nil {
			return fmt.Errorf("%s outcome unresolved; do not replay: %w", stage.name, err)
		}
		if err := record(stage.name+"_delivered", frame); err != nil {
			return fmt.Errorf("%s delivered; ledger failed, do not replay: %w", stage.name, err)
		}
		for {
			if !time.Now().Before(deadline) {
				return fmt.Errorf("%s outcome unresolved at 10-second deadline; do not replay", stage.name)
			}
			next, err := get()
			if err != nil {
				return fmt.Errorf("%s outcome unresolved; do not replay: %w", stage.name, err)
			}
			if !time.Now().Before(deadline) {
				return fmt.Errorf("%s outcome unresolved at 10-second deadline; do not replay", stage.name)
			}
			if next.Reference != frame.Reference && next.Phase == stage.phase {
				frame = next
				break
			}
			wait()
		}
		if err := record(stage.name+"_confirmed", frame); err != nil {
			return err
		}
	}
	return nil
}
