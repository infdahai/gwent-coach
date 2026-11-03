package observe

import (
	"errors"
	"fmt"
	"image"

	"gwentcoachmvp/internal/cardmatch"
)

// HandScanConfig describes explicitly calibrated native-pixel art regions in
// left-to-right order for one particular hand layout. It does not locate cards.
type HandScanConfig struct {
	Database, Templates string
	Width, Height       int
	Slots               []cardmatch.ROI
	MaxDistance, MinGap int
}

type HandCandidate struct {
	Slot   int              `json:"slot"`
	Art    cardmatch.ROI    `json:"art"`
	First  cardmatch.Result `json:"first"`
	Second cardmatch.Result `json:"second"`
	CardID int              `json:"card_id,omitempty"`
	Status string           `json:"status"`
}

// scanHandFrame shares slot matching between offline scans and the local match
// host. It supplies one frame only; the caller must establish temporal agreement.
func scanHandFrame(cfg HandScanConfig, matcher *cardmatch.Index, img image.Image, allowBlank bool) ([]cardmatch.Result, error) {
	if matcher == nil || cfg.Width <= 0 || cfg.Height <= 0 || len(cfg.Slots) == 0 || len(cfg.Slots) > 10 {
		return nil, fmt.Errorf("matcher, native dimensions and 1..10 calibrated slots required")
	}
	thresholds := cardmatch.Thresholds{MaxDistance: cfg.MaxDistance, MinGap: cfg.MinGap}
	if err := thresholds.Validate(); err != nil {
		return nil, err
	}
	if img == nil || img.Bounds() != image.Rect(0, 0, cfg.Width, cfg.Height) {
		return nil, fmt.Errorf("frame does not match calibrated native dimensions")
	}
	results := make([]cardmatch.Result, len(cfg.Slots))
	for i, roi := range cfg.Slots {
		feature, err := cardmatch.Normalize(img, roi)
		if allowBlank && errors.Is(err, cardmatch.ErrDegenerateArt) {
			results[i] = cardmatch.Result{Status: "unknown"}
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("slot %d: %w", i, err)
		}
		results[i], err = matcher.Match(feature, nil, thresholds)
		if err != nil {
			return nil, err
		}
	}
	return results, nil
}

// ScanHand evaluates the same calibrated regions in two native-size frames.
// Results are diagnostic candidates; even agreement is not a live-state proof.
func ScanHand(cfg HandScanConfig, matcher *cardmatch.Index, images [2]image.Image) ([]HandCandidate, error) {
	var matches [2][]cardmatch.Result
	for j, img := range images {
		var err error
		matches[j], err = scanHandFrame(cfg, matcher, img, false)
		if err != nil {
			return nil, fmt.Errorf("frame %d: %w", j, err)
		}
	}
	results := make([]HandCandidate, 0, len(cfg.Slots))
	for i, roi := range cfg.Slots {
		entry := HandCandidate{Slot: i, Art: roi, Status: "unknown", First: matches[0][i], Second: matches[1][i]}
		if entry.First.Status == "candidate" && entry.Second.Status == "candidate" && entry.First.RunnerDistance >= 0 && entry.Second.RunnerDistance >= 0 && entry.First.CardID == entry.Second.CardID {
			entry.Status, entry.CardID = "candidate", entry.First.CardID
		}
		results = append(results, entry)
	}
	return results, nil
}
