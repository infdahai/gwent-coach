package observe

import (
	"encoding/json"
	"fmt"
	"image"
	"io"
	"strings"
	"time"
	"unicode"

	"gwentcoachmvp/internal/cards"
)

type HoverSlot struct {
	Point   image.Point
	NameROI image.Rectangle
}
type HoverAnnotationConfig struct {
	ModelPath, ModelSHA256, StopFile, StateDir, Catalog, DeckAudit, ExpectedPhase string
	Slots                                                                         []HoverSlot
	NeutralPoint                                                                  image.Point
}
type auditDeck struct {
	DataVersion  string `json:"data_version"`
	RulesVersion string `json:"rules_version"`
	Cards        []struct {
		CardID string `json:"card_id"`
		Name   string `json:"name_cn"`
		Count  int    `json:"count"`
	} `json:"cards"`
}
type hoverResult struct {
	Slot       int       `json:"slot"`
	Status     string    `json:"status"`
	CardID     int       `json:"card_id,omitempty"`
	Name       string    `json:"name,omitempty"`
	Text       [2]string `json:"ocr_text"`
	Frames     [2]string `json:"frames"`
	DurationMS float64   `json:"duration_ms"`
}

func compactName(s string) string {
	compact := strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, strings.TrimSpace(s))
	// Windows OCR splits 仆 into its two components on the observed tooltip.
	return strings.ReplaceAll(compact, "亻卜", "仆")
}
func deckNames(catalog *cards.Catalog, audit auditDeck) (map[string]int, error) {
	if catalog == nil || catalog.Language != "cn" || catalog.Version != audit.DataVersion {
		return nil, fmt.Errorf("Chinese catalog and matching deck version required")
	}
	allowed := map[string]int{}
	for _, entry := range audit.Cards {
		var id int
		if _, err := fmt.Sscanf(entry.CardID, "%d", &id); err != nil {
			return nil, fmt.Errorf("invalid deck ID")
		}
		card, ok := catalog.Cards[id]
		if !ok || compactName(card.Name) != compactName(entry.Name) {
			return nil, fmt.Errorf("deck name mismatch for %d", id)
		}
		name := compactName(card.Name)
		if prev, ok := allowed[name]; ok && prev != id {
			return nil, fmt.Errorf("ambiguous deck name %q", name)
		}
		allowed[name] = id
	}
	if len(allowed) == 0 {
		return nil, fmt.Errorf("empty audited deck")
	}
	return allowed, nil
}

func matchDeckText(text string, names map[string]int) (int, string) {
	text = compactName(text)
	id, matched := 0, ""
	for name, candidate := range names {
		if strings.Contains(text, name) {
			if id != 0 && id != candidate {
				return 0, ""
			}
			id, matched = candidate, name
		}
	}
	return id, matched
}

func validateHoverConfig(cfg HoverAnnotationConfig, names map[string]int) error {
	if len(cfg.Slots) < 1 || len(cfg.Slots) > 10 || cfg.ExpectedPhase == "" || len(names) == 0 {
		return fmt.Errorf("calibrated slots, expected phase and audited names required")
	}
	bounds := image.Rect(0, 0, 1920, 1080)
	if cfg.NeutralPoint == (image.Point{}) || !cfg.NeutralPoint.In(bounds) {
		return fmt.Errorf("calibrated neutral point required")
	}
	for _, slot := range cfg.Slots {
		if slot.Point == (image.Point{}) || !slot.Point.In(bounds) || (!slot.NameROI.Empty() && !slot.NameROI.In(bounds)) {
			return fmt.Errorf("invalid hover point or name region")
		}
	}
	return nil
}

// runHoverAnnotation produces candidates, never trusted state. The capture and
// OCR callbacks must use fresh post-hover images; each slot gets two reads.
func runHoverAnnotation(cfg HoverAnnotationConfig, names map[string]int, capture func() (navFrame, error), hover func(navFrame, image.Point) error, ocr func(navFrame, image.Rectangle) (string, error), output io.Writer) error {
	if err := validateHoverConfig(cfg, names); err != nil {
		return err
	}
	bounds := image.Rect(0, 0, 1920, 1080)
	enc := json.NewEncoder(output)
	for i, slot := range cfg.Slots {
		start := time.Now()
		before, err := capture()
		if err != nil {
			return err
		}
		if before.Phase != cfg.ExpectedPhase || before.Width != 1920 || before.Height != 1080 || before.Reference == "" || time.Since(before.Captured) < 0 || time.Since(before.Captured) > 2*time.Second {
			return fmt.Errorf("slot %d requires fresh expected phase", i+1)
		}
		region := slot.NameROI
		if region.Empty() {
			region = bounds
		}
		if err := hover(before, cfg.NeutralPoint); err != nil {
			return fmt.Errorf("slot %d neutral hover unresolved: %w", i+1, err)
		}
		neutral, err := capture()
		if err != nil {
			return err
		}
		if neutral.Phase != cfg.ExpectedPhase || neutral.Reference == "" || neutral.Reference == before.Reference || neutral.Width != 1920 || neutral.Height != 1080 || time.Since(neutral.Captured) < 0 || time.Since(neutral.Captured) > 2*time.Second {
			return fmt.Errorf("slot %d requires a fresh neutral frame", i+1)
		}
		neutralText, err := ocr(neutral, region)
		if err != nil {
			return err
		}
		for name := range names {
			if strings.Contains(compactName(neutralText), name) {
				return fmt.Errorf("slot %d card tooltip remains visible at neutral point", i+1)
			}
		}
		before, err = capture()
		if err != nil {
			return err
		}
		if before.Phase != cfg.ExpectedPhase || before.Reference == "" || before.Reference == neutral.Reference || before.Width != 1920 || before.Height != 1080 || time.Since(before.Captured) < 0 || time.Since(before.Captured) > 2*time.Second {
			return fmt.Errorf("slot %d requires a fresh pre-hover frame", i+1)
		}
		if err := hover(before, slot.Point); err != nil {
			return fmt.Errorf("slot %d hover unresolved: %w", i+1, err)
		}
		r := hoverResult{Slot: i + 1, Status: "unknown"}
		var ids [2]int
		var matched [2]string
		for j := 0; j < 2; j++ {
			frame, err := capture()
			if err != nil {
				return err
			}
			if frame.Reference == "" || frame.Reference == before.Reference || (j > 0 && frame.Reference == r.Frames[0]) || frame.Width != 1920 || frame.Height != 1080 || time.Since(frame.Captured) < 0 || time.Since(frame.Captured) > 2*time.Second {
				return fmt.Errorf("slot %d requires fresh distinct post-hover frames", i+1)
			}
			r.Frames[j] = frame.Reference
			r.Text[j], err = ocr(frame, region)
			if err != nil {
				return err
			}
			ids[j], matched[j] = matchDeckText(r.Text[j], names)
		}
		if ids[0] != 0 && ids[0] == ids[1] {
			r.Status = "candidate"
			r.CardID = ids[0]
			r.Name = matched[0]
		}
		r.DurationMS = float64(time.Since(start)) / float64(time.Millisecond)
		if err := enc.Encode(r); err != nil {
			return err
		}
	}
	return nil
}
