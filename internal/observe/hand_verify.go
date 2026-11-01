package observe

import (
	"fmt"
	"image"
	"time"

	"gwentcoachmvp/internal/cardmatch"
	"gwentcoachmvp/internal/rules"
	"gwentcoachmvp/internal/stateestimate"
)

// HandFrame is a native frame with an independent capture identity and time.
type HandFrame struct {
	Source     string
	CapturedAt time.Time
	Image      image.Image
}

// SemanticFromHandEvidence combines independently calibrated game facts with
// a two-frame pixel check of every hand identity. It never constructs card
// instances from art; partial terminal outcomes remain read-only.
func SemanticFromHandEvidence(pair [2]stateestimate.Evidence, frames [2]HandFrame, cfg HandScanConfig, matcher *cardmatch.Index, now time.Time, maxAge time.Duration) (SemanticObservation, error) {
	for i := range frames {
		if frames[i].Source != pair[i].Frame.ID || !frames[i].CapturedAt.Equal(pair[i].Frame.CapturedAt) {
			return SemanticObservation{}, fmt.Errorf("hand images must match evidence frames")
		}
	}
	o, err := SemanticFromEvidence(pair, now, maxAge)
	if err != nil || o.PartialResult != nil || o.Result {
		return o, err
	}
	if o.View.HandCount[o.Player] == 0 {
		return o, nil
	}
	if err := verifyObservedHand(cfg, matcher, frames, o, now, maxAge); err != nil {
		return SemanticObservation{}, fmt.Errorf("hand evidence: %w", err)
	}
	return o, nil
}

// VerifyObservedHand checks every visible hand instance and its order against
// two captured images. The supplied observation must independently establish
// turn, board, counts and all other public state; card art cannot prove them.
// CardID zero preserves an explicitly unknown slot; it does not authorize a
// decision and still requires a usable image region.
func VerifyObservedHand(cfg HandScanConfig, matcher *cardmatch.Index, frames [2]HandFrame, observation SemanticObservation) error {
	return verifyObservedHand(cfg, matcher, frames, observation, time.Now(), 2*time.Second)
}

func verifyObservedHand(cfg HandScanConfig, matcher *cardmatch.Index, frames [2]HandFrame, observation SemanticObservation, now time.Time, maxAge time.Duration) error {
	if frames[0].Source == "" || frames[1].Source == "" || frames[0].Source == frames[1].Source || observation.Source != frames[1].Source || !observation.CapturedAt.Equal(frames[1].CapturedAt) {
		return fmt.Errorf("two distinct frames bound to the observation required")
	}
	for _, frame := range frames {
		if age := now.Sub(frame.CapturedAt); frame.CapturedAt.IsZero() || age < 0 || age > maxAge {
			return fmt.Errorf("stale hand frame")
		}
	}
	if observation.Player < 0 || observation.Player > 1 || len(observation.HandOrder) != len(cfg.Slots) || observation.View.HandCount[observation.Player] != len(cfg.Slots) {
		return fmt.Errorf("incomplete hand observation")
	}
	candidates, err := ScanHand(cfg, matcher, [2]image.Image{frames[0].Image, frames[1].Image})
	if err != nil {
		return err
	}
	hand := make(map[string]int)
	for _, card := range observation.View.Visible {
		if card.Zone == rules.Hand && card.Owner == observation.Player {
			if _, exists := hand[card.ID]; card.ID == "" || exists {
				return fmt.Errorf("invalid hand instance")
			}
			hand[card.ID] = card.CardID
		}
	}
	if len(hand) != len(candidates) {
		return fmt.Errorf("incomplete visible hand")
	}
	seen := make(map[string]bool)
	for i, id := range observation.HandOrder {
		cardID, ok := hand[id]
		if !ok || seen[id] || (cardID != 0 && (candidates[i].Status != "candidate" || candidates[i].CardID != cardID)) {
			return fmt.Errorf("hand slot %d identity unresolved or inconsistent", i)
		}
		seen[id] = true
	}
	return nil
}
