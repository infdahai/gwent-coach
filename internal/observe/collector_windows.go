//go:build windows

package observe

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"
)

// RunReadOnlyCollection uses existing guarded capture reports and never sends input.
func RunReadOnlyCollection(out string, logger *log.Logger, configPath, stopFile string, count int, interval time.Duration) error {
	if stopFile == "" {
		return fmt.Errorf("stop file path required")
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}
	var cfg CollectionConfig
	if err = json.Unmarshal(data, &cfg); err != nil {
		return err
	}
	cfg.Count = count
	cfg.Interval = interval
	if cfg.Count < 1 || cfg.Count > 100 || cfg.Interval < 100*time.Millisecond || len(cfg.Digits) == 0 {
		return fmt.Errorf("bounded collection and at least one candidate calibration required")
	}
	dir, err := os.MkdirTemp(out, "readonly-collection-")
	if err != nil {
		return err
	}
	file, err := os.OpenFile(filepath.Join(dir, "recognition.jsonl"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer file.Close()
	capture := func(ctx context.Context) (CollectionFrame, error) {
		if err := ctx.Err(); err != nil {
			return CollectionFrame{}, err
		}
		if err := stopped(stopFile); err != nil {
			return CollectionFrame{}, err
		}
		frameDir, err := os.MkdirTemp(dir, "frame-")
		if err != nil {
			return CollectionFrame{}, err
		}
		if err = runSample(frameDir, logger, Sampling{JPEGQuality: 85}); err != nil {
			return CollectionFrame{}, err
		}
		paths, err := filepath.Glob(filepath.Join(frameDir, "*", "report.json"))
		if err != nil || len(paths) != 1 {
			return CollectionFrame{}, fmt.Errorf("missing capture report")
		}
		raw, err := os.ReadFile(paths[0])
		if err != nil {
			return CollectionFrame{}, err
		}
		var r report
		if err = json.Unmarshal(raw, &r); err != nil {
			return CollectionFrame{}, err
		}
		captured, err := time.Parse(time.RFC3339Nano, r.CapturedAt)
		if err != nil {
			return CollectionFrame{}, err
		}
		img, err := readImage(r.Screenshot)
		if err != nil {
			return CollectionFrame{}, err
		}
		return CollectionFrame{Reference: paths[0], Captured: captured, Image: img}, nil
	}
	return CollectReadOnly(context.Background(), cfg, capture, file)
}
