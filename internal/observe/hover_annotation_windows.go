//go:build windows

package observe

import (
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"

	"gwentcoachmvp/internal/cardindex"
	"gwentcoachmvp/internal/cards"
)

// RunHoverAnnotation performs one bounded local pass over calibrated slots.
// Its OCR matches are candidates; it never sends a click or plays a card.
func RunHoverAnnotation(out string, logger *log.Logger, configPath string, output io.Writer) error {
	var cfg HoverAnnotationConfig
	if err := cardindex.ReadJSON(configPath, &cfg); err != nil {
		return err
	}
	resolve := func(p string) string {
		if filepath.IsAbs(p) {
			return p
		}
		return filepath.Join(filepath.Dir(configPath), p)
	}
	if cfg.StateDir == "" || !strings.EqualFold(filepath.Clean(cfg.StopFile), `D:\gwent-coach-training-local\PAUSE`) {
		return fmt.Errorf("new state directory and fixed training stop file required")
	}
	cfg.ModelPath = resolve(cfg.ModelPath)
	model, err := loadBoundPhases(cfg.ModelPath, cfg.ModelSHA256)
	if err != nil {
		return err
	}
	catalog, err := cards.Load(resolve(cfg.Catalog))
	if err != nil {
		return err
	}
	var audit auditDeck
	if err := cardindex.ReadJSON(resolve(cfg.DeckAudit), &audit); err != nil {
		return err
	}
	names, err := deckNames(catalog, audit)
	if err != nil {
		return err
	}
	if err := validateHoverConfig(cfg, names); err != nil {
		return err
	}
	if err := os.Mkdir(cfg.StateDir, 0700); err != nil {
		return err
	}
	file, err := os.OpenFile(filepath.Join(cfg.StateDir, "annotations.jsonl"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer file.Close()
	worker, err := startOCR(logger.Writer())
	if err != nil {
		return err
	}
	defer worker.close()
	if err := acquireInterlock(`D:\gwent-coach-training-local\training-autoplay-interlock`); err != nil {
		return err
	}
	capture := func() (navFrame, error) {
		if err := stopped(cfg.StopFile); err != nil {
			return navFrame{}, err
		}
		dir, err := os.MkdirTemp(out, "hover-frame-")
		if err != nil {
			return navFrame{}, err
		}
		if err := runSample(dir, logger, Sampling{JPEGQuality: 85}); err != nil {
			return navFrame{}, err
		}
		paths, err := filepath.Glob(filepath.Join(dir, "*", "report.json"))
		if err != nil || len(paths) != 1 {
			return navFrame{}, fmt.Errorf("missing hover capture report")
		}
		return trainingFrame(paths[0], model)
	}
	hover := func(frame navFrame, p image.Point) error {
		return Execute(out, logger, Action{Kind: "hover", X: p.X, Y: p.Y, Reference: frame.Reference, StopFile: cfg.StopFile, PhaseModel: cfg.ModelPath, ExpectedPhase: cfg.ExpectedPhase, phaseModel: model})
	}
	ocr := func(frame navFrame, roi image.Rectangle) (string, error) {
		data, err := os.ReadFile(frame.Reference)
		if err != nil {
			return "", err
		}
		var r report
		if err := json.Unmarshal(data, &r); err != nil {
			return "", err
		}
		img, err := readImage(r.Screenshot)
		if err != nil {
			return "", err
		}
		if !roi.In(img.Bounds()) {
			return "", fmt.Errorf("name region outside screenshot")
		}
		sub, ok := img.(interface {
			SubImage(image.Rectangle) image.Image
		})
		if !ok {
			return "", fmt.Errorf("screenshot cannot be cropped")
		}
		f, err := os.CreateTemp(cfg.StateDir, "ocr-name-*.png")
		if err != nil {
			return "", err
		}
		name, err := filepath.Abs(f.Name())
		if err != nil {
			f.Close()
			return "", err
		}
		if err := png.Encode(f, sub.SubImage(roi)); err != nil {
			f.Close()
			return "", err
		}
		if err := f.Close(); err != nil {
			return "", err
		}
		return worker.recognize(name)
	}
	return runHoverAnnotation(cfg, names, capture, hover, ocr, syncedSliceWriter{file, output})
}
