package observe

import (
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

// ScanHand evaluates the same calibrated regions in two native-size frames.
// Results are diagnostic candidates; even agreement is not a live-state proof.
func ScanHand(cfg HandScanConfig, matcher *cardmatch.Index, images [2]image.Image) ([]HandCandidate, error) {
	if matcher == nil || cfg.Width <= 0 || cfg.Height <= 0 || len(cfg.Slots) == 0 || len(cfg.Slots) > 10 {
		return nil, fmt.Errorf("matcher, native dimensions and 1..10 calibrated slots required")
	}
	thresholds := cardmatch.Thresholds{MaxDistance: cfg.MaxDistance, MinGap: cfg.MinGap}
	if err := thresholds.Validate(); err != nil {
		return nil, err
	}
	for _, img := range images {
		if img == nil || img.Bounds() != image.Rect(0, 0, cfg.Width, cfg.Height) {
			return nil, fmt.Errorf("frame does not match calibrated native dimensions")
		}
	}
	results := make([]HandCandidate, 0, len(cfg.Slots))
	for i, roi := range cfg.Slots {
		entry := HandCandidate{Slot: i, Art: roi, Status: "unknown"}
		matches := [2]cardmatch.Result{}
		for j, img := range images {
			feature, err := cardmatch.Normalize(img, roi)
			if err != nil {
				return nil, fmt.Errorf("slot %d frame %d: %w", i, j, err)
			}
			matches[j], err = matcher.Match(feature, nil, thresholds)
			if err != nil {
				return nil, fmt.Errorf("slot %d frame %d: %w", i, j, err)
			}
		}
		entry.First, entry.Second = matches[0], matches[1]
		if matches[0].Status == "candidate" && matches[1].Status == "candidate" && matches[0].RunnerDistance >= 0 && matches[1].RunnerDistance >= 0 && matches[0].CardID == matches[1].CardID {
			entry.Status, entry.CardID = "candidate", matches[0].CardID
		}
		results = append(results, entry)
	}
	return results, nil
}
