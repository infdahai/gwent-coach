//go:build windows

package observe

import (
	"encoding/json"
	"fmt"
	"image"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type syncedSliceWriter struct {
	file   *os.File
	output io.Writer
}

func (w syncedSliceWriter) Write(p []byte) (int, error) {
	n, err := w.file.Write(p)
	if err != nil {
		return n, err
	}
	if err := w.file.Sync(); err != nil {
		return n, err
	}
	if _, err := w.output.Write(p); err != nil {
		return n, err
	}
	return n, nil
}

// RunNativeSlice executes one calibrated opening in the interactive session.
func RunNativeSlice(out string, logger *log.Logger, configPath string, output io.Writer) error {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}
	var cfg NativeSliceConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return err
	}
	if !filepath.IsAbs(cfg.ModelPath) {
		cfg.ModelPath = filepath.Join(filepath.Dir(configPath), cfg.ModelPath)
	}
	if !strings.EqualFold(filepath.Clean(cfg.StopFile), `D:\gwent-coach-training-local\PAUSE`) {
		return fmt.Errorf("slice requires fixed training stop file")
	}
	if cfg.StateDir == "" {
		return fmt.Errorf("new slice state directory required")
	}
	model, err := loadBoundPhases(cfg.ModelPath, cfg.ModelSHA256)
	if err != nil {
		return err
	}
	phases := map[string]bool{}
	for _, p := range model.Phases {
		phases[p.Name] = true
	}
	for _, name := range []string{"slice-ready", "slice-selected", "slice-placed", "slice-ended"} {
		if !phases[name] {
			return fmt.Errorf("calibrated phase %s required", name)
		}
	}
	if err := os.Mkdir(cfg.StateDir, 0700); err != nil {
		return err
	}
	file, err := os.OpenFile(filepath.Join(cfg.StateDir, "events.jsonl"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer file.Close()
	if err := acquireInterlock(`D:\gwent-coach-training-local\training-autoplay-interlock`); err != nil {
		return err
	}
	capture := func() (navFrame, error) {
		dir, err := os.MkdirTemp(out, "slice-frame-")
		if err != nil {
			return navFrame{}, err
		}
		if err := runSample(dir, logger, Sampling{}); err != nil {
			return navFrame{}, err
		}
		paths, err := filepath.Glob(filepath.Join(dir, "*", "report.json"))
		if err != nil || len(paths) != 1 {
			return navFrame{}, fmt.Errorf("missing slice report")
		}
		return trainingFrame(paths[0], model)
	}
	send := func(frame navFrame, p image.Point, deadline time.Time) error {
		return Execute(out, logger, Action{Kind: "click", X: p.X, Y: p.Y, Reference: frame.Reference, StopFile: cfg.StopFile, PhaseModel: cfg.ModelPath, ExpectedPhase: frame.Phase, phaseModel: model, deadline: deadline})
	}
	return runNativeSlice(cfg, capture, send, func() error { return stopped(cfg.StopFile) }, func() { time.Sleep(100 * time.Millisecond) }, syncedSliceWriter{file, output})
}
