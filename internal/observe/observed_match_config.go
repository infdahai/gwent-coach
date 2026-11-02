package observe

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	"os"
	"path/filepath"

	"gwentcoachmvp/internal/cardindex"
	"gwentcoachmvp/internal/cardmatch"
)

const ObservedMatchKind = "observed-match-v1"

// ObservedMatchConfig describes UI evidence, not a simulated board or result.
// Hand identities are local candidates; independent board/interaction evidence
// must establish each selected card's actual placement.
type ObservedMatchConfig struct {
	Kind, ID, DeckID, ModelPath, ModelSHA256, StopFile, StateDir string
	TimeoutMS, TransitionTimeoutMS, UnknownTimeoutMS             int
	Start, FinishMulligan, ConfirmMulligan, Pass, EndTurn        image.Point
	Exit                                                         *image.Point `json:",omitempty"`
	Hand                                                         ObservedHandConfig
	Plays                                                        []ObservedPlay
	model                                                        *PhaseModel
	configSHA256                                                 string
}

type ObservedPlay struct {
	ID, ModelPath, ModelSHA256 string
	CardID                     int
	Row                        image.Point
	BoardROI                   image.Rectangle
	model                      *PhaseModel
}

type ObservedHandConfig struct {
	Database, Templates, TemplatesSHA256 string
	Slots, GuardSlots                    []cardmatch.ROI
	Clicks                               []image.Point
	MaxDistance, MinGap                  int
	matcher                              *cardmatch.Index
}

func (h ObservedHandConfig) scanConfig(guard bool) HandScanConfig {
	slots := h.Slots
	if guard {
		slots = h.GuardSlots
	}
	return HandScanConfig{Width: 1920, Height: 1080, Slots: slots, MaxDistance: h.MaxDistance, MinGap: h.MinGap}
}

// These units have no deploy choice or target. Passive/order effects are left
// to the game; this policy neither activates orders nor predicts their effects.
func safeObservedUnit(id int) bool {
	switch id {
	case 201676, 142315, 203073, 200039, 203152, 142306:
		return true
	}
	return false
}

func LoadObservedMatchConfig(path string) (ObservedMatchConfig, error) {
	var cfg ObservedMatchConfig
	data, err := os.ReadFile(path)
	if err != nil {
		return cfg, err
	}
	if err = json.Unmarshal(data, &cfg); err != nil {
		return cfg, err
	}
	sum := sha256.Sum256(data)
	cfg.configSHA256 = hex.EncodeToString(sum[:])
	resolve := func(p string) string {
		if filepath.IsAbs(p) {
			return p
		}
		return filepath.Join(filepath.Dir(path), p)
	}
	if cfg.Kind != ObservedMatchKind || !safeSessionID(cfg.ID) || cfg.DeckID == "" || cfg.StateDir == "" || !filepath.IsAbs(cfg.StopFile) || cfg.TimeoutMS <= 0 || cfg.TransitionTimeoutMS <= 0 || cfg.UnknownTimeoutMS <= 0 || cfg.UnknownTimeoutMS > cfg.TransitionTimeoutMS || len(cfg.Plays) == 0 {
		return cfg, fmt.Errorf("complete observed-match configuration required")
	}
	cfg.StateDir, cfg.ModelPath = resolve(cfg.StateDir), resolve(cfg.ModelPath)
	cfg.model, err = loadBoundPhases(cfg.ModelPath, cfg.ModelSHA256)
	if err != nil {
		return cfg, err
	}
	if err = requireObservedPhases(cfg.model, "training", "mulligan", "mulligan-confirm", "turn", "passed"); err != nil {
		return cfg, err
	}
	result := false
	for _, p := range cfg.model.Phases {
		switch p.Name {
		case "victory", "defeat", "draw":
			result = true
		case "training", "mulligan", "mulligan-confirm", "turn", "passed", "opponent", "transition":
		default:
			return cfg, fmt.Errorf("unsupported lifecycle phase %q", p.Name)
		}
	}
	if !result {
		return cfg, fmt.Errorf("at least one observed result phase required")
	}
	bounds := image.Rect(0, 0, 1920, 1080)
	for _, p := range []image.Point{cfg.Start, cfg.FinishMulligan, cfg.ConfirmMulligan, cfg.Pass, cfg.EndTurn} {
		if p == (image.Point{}) || !p.In(bounds) {
			return cfg, fmt.Errorf("calibrated input points required")
		}
	}
	if cfg.Exit != nil && (*cfg.Exit == (image.Point{}) || !cfg.Exit.In(bounds)) {
		return cfg, fmt.Errorf("invalid exit point")
	}
	h := &cfg.Hand
	if len(h.Slots) < 1 || len(h.Slots) > 10 || len(h.Clicks) != len(h.Slots) || len(h.GuardSlots) != len(h.Slots) {
		return cfg, fmt.Errorf("1..10 calibrated hand slots, guard slots and click points required")
	}
	if err := (cardmatch.Thresholds{MaxDistance: h.MaxDistance, MinGap: h.MinGap}).Validate(); err != nil {
		return cfg, err
	}
	for i, roi := range h.Slots {
		for _, r := range []cardmatch.ROI{roi, h.GuardSlots[i]} {
			if r.Width < 9 || r.Height < 8 || !image.Rect(r.X, r.Y, r.X+r.Width, r.Y+r.Height).In(bounds) {
				return cfg, fmt.Errorf("invalid hand art region")
			}
		}
		if !h.Clicks[i].In(image.Rect(roi.X, roi.Y, roi.X+roi.Width, roi.Y+roi.Height)) {
			return cfg, fmt.Errorf("hand click must be inside its observed art region")
		}
	}
	h.Database, h.Templates = resolve(h.Database), resolve(h.Templates)
	db, err := cardindex.Open(h.Database, cardindex.Version{Data: cardindex.DataVersion, Rules: cardindex.RulesVersion})
	if err != nil {
		return cfg, err
	}
	templatesData, err := os.ReadFile(h.Templates)
	if err != nil {
		return cfg, err
	}
	templateSum := sha256.Sum256(templatesData)
	if hex.EncodeToString(templateSum[:]) != h.TemplatesSHA256 {
		return cfg, fmt.Errorf("hand template digest mismatch")
	}
	var templates cardmatch.File
	if err = json.Unmarshal(templatesData, &templates); err != nil {
		return cfg, err
	}
	h.matcher, err = cardmatch.New(templates, db)
	if err != nil {
		return cfg, err
	}
	templateIDs := map[int]bool{}
	for _, template := range templates.Templates {
		templateIDs[template.CardID] = true
	}
	if len(templateIDs) < 2 {
		return cfg, fmt.Errorf("hand identity requires competing card templates")
	}
	seen := map[string]bool{}
	for i := range cfg.Plays {
		p := &cfg.Plays[i]
		if !safeSessionID(p.ID) || seen[p.ID] || !safeObservedUnit(p.CardID) || !templateIDs[p.CardID] || !p.Row.In(p.BoardROI) || p.BoardROI.Empty() || !p.BoardROI.In(bounds) {
			return cfg, fmt.Errorf("invalid or unsupported safe play %q", p.ID)
		}
		seen[p.ID] = true
		p.ModelPath = resolve(p.ModelPath)
		p.model, err = loadBoundPhases(p.ModelPath, p.ModelSHA256)
		if err != nil {
			return cfg, err
		}
		if err = requireObservedPhases(p.model, "ready", "selected", "ended"); err != nil {
			return cfg, err
		}
		for _, phase := range p.model.Phases {
			if phase.Name != "ready" && phase.Name != "selected" && phase.Name != "placed" && phase.Name != "ended" {
				return cfg, fmt.Errorf("unsupported play phase %q", phase.Name)
			}
			if !p.model.RequiresRGBAnchor(phase.Name, p.BoardROI, 0.15) {
				return cfg, fmt.Errorf("play %s phase %s must bind board RGB region", p.ID, phase.Name)
			}
		}
	}
	return cfg, nil
}

func guardObservedCard(img image.Image, hand ObservedHandConfig, slot, cardID int) error {
	if slot < 0 || slot >= len(hand.GuardSlots) {
		return fmt.Errorf("invalid guarded hand slot")
	}
	cfg := hand.scanConfig(true)
	cfg.Slots = cfg.Slots[slot : slot+1]
	matches, err := scanHandFrame(cfg, hand.matcher, img, false)
	if err != nil {
		return err
	}
	if matches[0].Status != "candidate" || matches[0].CardID != cardID || matches[0].RunnerDistance < 0 {
		return fmt.Errorf("guarded hand identity changed or unknown")
	}
	return nil
}

func requireObservedPhases(model *PhaseModel, names ...string) error {
	if model.Width != 1920 || model.Height != 1080 {
		return fmt.Errorf("observed match requires native 1920x1080 model")
	}
	for _, name := range names {
		found := false
		for _, p := range model.Phases {
			if p.Name == name {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("calibrated phase %s required", name)
		}
	}
	return nil
}
