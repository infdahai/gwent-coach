package observe

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// ReplayManifest lists manually labelled frames. Paths are relative to the manifest.
// An expected phase of "unknown" marks a negative or ambiguous example.
type ReplayManifest struct {
	Frames []ReplayFrame `json:"frames"`
}

type ReplayFrame struct {
	Image    string `json:"image"`
	Expected string `json:"expected"`
}

type ReplayResult struct {
	Image      string       `json:"image"`
	Expected   string       `json:"expected"`
	Observed   string       `json:"observed"`
	Correct    bool         `json:"correct"`
	DurationMS float64      `json:"duration_ms"`
	Scores     []PhaseScore `json:"scores"`
}

// ReplayPhases classifies each labelled frame and emits one JSON record per frame.
// It does not send input or treat a successful replay as live validation.
func ReplayPhases(modelPath, manifestPath string, out io.Writer) error {
	model, err := LoadPhases(modelPath)
	if err != nil {
		return err
	}
	b, err := os.ReadFile(manifestPath)
	if err != nil {
		return err
	}
	var manifest ReplayManifest
	if err := json.Unmarshal(b, &manifest); err != nil {
		return err
	}
	if len(manifest.Frames) == 0 {
		return fmt.Errorf("replay has no frames")
	}
	known := map[string]bool{"unknown": true}
	for _, phase := range model.Phases {
		known[phase.Name] = true
	}
	enc := json.NewEncoder(out)
	for _, frame := range manifest.Frames {
		if frame.Image == "" || !known[frame.Expected] {
			return fmt.Errorf("invalid replay frame or expected phase %q", frame.Expected)
		}
		img, err := readImage(filepath.Join(filepath.Dir(manifestPath), frame.Image))
		if err != nil {
			return err
		}
		start := time.Now()
		phase, scores := model.Recognize(img)
		result := ReplayResult{Image: frame.Image, Expected: frame.Expected, Observed: phase, Correct: phase == frame.Expected, DurationMS: float64(time.Since(start).Microseconds()) / 1000, Scores: scores}
		if err := enc.Encode(result); err != nil {
			return err
		}
	}
	return nil
}
