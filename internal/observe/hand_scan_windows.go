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
	"time"

	"gwentcoachmvp/internal/cardindex"
	"gwentcoachmvp/internal/cardmatch"
)

// RunHandScan captures two Windows frames and reports calibrated art candidates.
// It does not send input or infer turn eligibility.
func RunHandScan(out string, logger *log.Logger, configPath, stopFile string, count int, interval time.Duration, output io.Writer) error {
	if err := (Sampling{Count: count, Interval: interval, StopFile: stopFile}).Validate(); err != nil {
		return err
	}
	if stopFile == "" {
		return fmt.Errorf("hand scan requires a stop file path")
	}
	var cfg HandScanConfig
	if err := cardindex.ReadJSON(configPath, &cfg); err != nil {
		return err
	}
	resolve := func(path string) string {
		if filepath.IsAbs(path) {
			return path
		}
		return filepath.Join(filepath.Dir(configPath), path)
	}
	version := cardindex.Version{Data: cardindex.DataVersion, Rules: cardindex.RulesVersion}
	db, err := cardindex.Open(resolve(cfg.Database), version)
	if err != nil {
		return err
	}
	var templates cardmatch.File
	if err := cardindex.ReadJSON(resolve(cfg.Templates), &templates); err != nil {
		return err
	}
	matcher, err := cardmatch.New(templates, db)
	if err != nil {
		return err
	}
	for scan := 0; scan < count; scan++ {
		started := time.Now()
		var images [2]image.Image
		var reports [2]string
		var captureMS [2]float64
		for i := range images {
			captureStart := time.Now()
			if err := stopped(stopFile); err != nil {
				return err
			}
			dir, err := os.MkdirTemp(out, "hand-frame-")
			if err != nil {
				return err
			}
			if err := runSample(dir, logger, Sampling{}); err != nil {
				return err
			}
			paths, err := filepath.Glob(filepath.Join(dir, "*", "report.json"))
			if err != nil || len(paths) != 1 {
				return fmt.Errorf("missing hand capture report")
			}
			b, err := os.ReadFile(paths[0])
			if err != nil {
				return err
			}
			var r report
			if err := json.Unmarshal(b, &r); err != nil {
				return err
			}
			images[i], err = readImage(r.Screenshot)
			if err != nil {
				return err
			}
			reports[i] = paths[0]
			captureMS[i] = float64(time.Since(captureStart)) / float64(time.Millisecond)
			if i == 0 {
				time.Sleep(500 * time.Millisecond)
			}
		}
		recognitionStart := time.Now()
		candidates, err := ScanHand(cfg, matcher, images)
		if err != nil {
			return err
		}
		recognitionMS := float64(time.Since(recognitionStart)) / float64(time.Millisecond)
		if err := json.NewEncoder(output).Encode(struct {
			Status        string          `json:"status"`
			Reports       [2]string       `json:"reports"`
			Cards         []HandCandidate `json:"cards"`
			CaptureMS     [2]float64      `json:"capture_ms"`
			RecognitionMS float64         `json:"recognition_ms"`
			TotalMS       float64         `json:"total_ms"`
		}{"unvalidated_candidates_no_input", reports, candidates, captureMS, recognitionMS, float64(time.Since(started)) / float64(time.Millisecond)}); err != nil {
			return err
		}
		if scan+1 < count {
			time.Sleep(max(0, interval-time.Since(started)))
		}
	}
	return nil
}
