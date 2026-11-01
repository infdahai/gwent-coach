package observe

import (
	"context"
	"encoding/json"
	"fmt"
	"image"
	"io"
	"sort"
	"time"

	"gwentcoachmvp/internal/stateestimate"
)

// CollectionFrame is a captured image and its immutable report reference.
type CollectionFrame struct {
	Reference string
	Captured  time.Time
	Image     image.Image
}
type CollectionConfig struct {
	Count    int
	Interval time.Duration
	Digits   map[string]DigitCalibration
}
type CollectionRecord struct {
	Trust     string         `json:"trust"`
	Reference string         `json:"reference"`
	Captured  time.Time      `json:"captured_at"`
	Scalars   map[string]int `json:"scalars"`
	Unknown   []string       `json:"unknown"`
	Agreed    map[string]int `json:"agreed,omitempty"`
}

const maxCollectionFrames = 1200

// CollectReadOnly captures a bounded number of frames; capture owns platform safety.
func CollectReadOnly(ctx context.Context, cfg CollectionConfig, capture func(context.Context) (CollectionFrame, error), output io.Writer) error {
	if cfg.Count < 1 || cfg.Count > maxCollectionFrames || cfg.Interval < 100*time.Millisecond || capture == nil || output == nil {
		return fmt.Errorf("collection requires 1..%d frames and interval >=100ms", maxCollectionFrames)
	}
	names := make([]string, 0, len(cfg.Digits))
	for name := range cfg.Digits {
		if name == "" || !cfg.Digits[name].CandidateOnly {
			return fmt.Errorf("digit name and explicit candidate-only calibration required")
		}
		if cfg.Digits[name].Method != "area-v1" {
			return fmt.Errorf("digit %q: calibration requires Method area-v1 and recalibrated templates", name)
		}
		names = append(names, name)
	}
	sort.Strings(names)
	enc := json.NewEncoder(output)
	var previous *stateestimate.Evidence
	for i := 0; i < cfg.Count; i++ {
		start := time.Now()
		frame, err := capture(ctx)
		if err != nil {
			return err
		}
		if frame.Image == nil || frame.Reference == "" || frame.Captured.IsZero() {
			return fmt.Errorf("invalid captured frame")
		}
		record := CollectionRecord{Trust: "unverified_candidate_not_for_actions", Reference: frame.Reference, Captured: frame.Captured, Scalars: map[string]int{}, Unknown: []string{}}
		for _, name := range names {
			if value, ok := cfg.Digits[name].ReadDigits(frame.Image); ok {
				record.Scalars[name] = value
			} else {
				record.Unknown = append(record.Unknown, name)
			}
		}
		current := stateestimate.Evidence{Frame: stateestimate.FrameRef{ID: frame.Reference, MatchID: "collection", CapturedAt: frame.Captured, Width: frame.Image.Bounds().Dx(), Height: frame.Image.Bounds().Dy()}, CalibrationID: "collection", Scalars: record.Scalars}
		if previous != nil {
			estimated, e := stateestimate.Estimate([2]stateestimate.Evidence{*previous, current}, time.Now(), 2*cfg.Interval+2*time.Second)
			if e != nil {
				return e
			}
			record.Agreed = estimated.Scalars
		}
		if err = enc.Encode(record); err != nil {
			return err
		}
		previous = &current
		if i+1 < cfg.Count {
			timer := time.NewTimer(max(0, cfg.Interval-time.Since(start)))
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}
	}
	return nil
}
