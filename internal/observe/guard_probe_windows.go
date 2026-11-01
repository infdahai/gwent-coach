//go:build windows

package observe

import (
	"fmt"
	"image"
	"log"
	"os"
	"path/filepath"
	"strings"

	"gwentcoachmvp/internal/cardindex"
)

// RunGuardProbe moves the cursor and records the exact guard frame without
// emitting any mouse button events. The shared interlock remains held afterward.
func RunGuardProbe(out string, logger *log.Logger, configPath string) error {
	var cfg NativeLoopConfig
	if err := cardindex.ReadJSON(configPath, &cfg); err != nil {
		return err
	}
	if !strings.EqualFold(filepath.Clean(cfg.StopFile), `D:\gwent-coach-training-local\PAUSE`) || cfg.ResumeSelectedPhase == "" {
		return fmt.Errorf("fixed stop file and selected phase required")
	}
	if !filepath.IsAbs(cfg.ModelPath) {
		cfg.ModelPath = filepath.Join(filepath.Dir(configPath), cfg.ModelPath)
	}
	model, err := loadBoundPhases(cfg.ModelPath, cfg.ModelSHA256)
	if err != nil {
		return err
	}
	if err := acquireInterlock(`D:\gwent-coach-training-local\training-autoplay-interlock`); err != nil {
		return err
	}
	dir, err := os.MkdirTemp(out, "guard-reference-")
	if err != nil {
		return err
	}
	if err := runSample(dir, logger, Sampling{JPEGQuality: 85}); err != nil {
		return err
	}
	paths, err := filepath.Glob(filepath.Join(dir, "*", "report.json"))
	if err != nil || len(paths) != 1 {
		return fmt.Errorf("missing guard reference")
	}
	frame, err := trainingFrame(paths[0], model)
	if err != nil {
		return err
	}
	if frame.Phase != cfg.ResumeSelectedPhase || frame.Width != 1920 || frame.Height != 1080 {
		return fmt.Errorf("expected selected phase before probe, got %s", frame.Phase)
	}
	if cfg.GuardProbeStart != (image.Point{}) {
		if err := Execute(out, logger, Action{Kind: "hover", X: cfg.GuardProbeStart.X, Y: cfg.GuardProbeStart.Y, Reference: frame.Reference, StopFile: cfg.StopFile}); err != nil {
			return err
		}
		dir, err := os.MkdirTemp(out, "guard-reference-")
		if err != nil {
			return err
		}
		if err := runSample(dir, logger, Sampling{JPEGQuality: 85}); err != nil {
			return err
		}
		paths, err := filepath.Glob(filepath.Join(dir, "*", "report.json"))
		if err != nil || len(paths) != 1 {
			return fmt.Errorf("missing moved-cursor reference")
		}
		frame, err = trainingFrame(paths[0], model)
		if err != nil {
			return err
		}
	}
	p := cfg.RowPoints[0]
	return Execute(out, logger, Action{Kind: "hover", X: p.X, Y: p.Y, Reference: frame.Reference, StopFile: cfg.StopFile, PhaseModel: cfg.ModelPath, ExpectedPhase: cfg.ResumeSelectedPhase, phaseModel: model, guardProbe: true})
}
