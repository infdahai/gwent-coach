package cardmatch

import (
	"fmt"
	"image"

	"gwentcoachmvp/internal/rules"
)

// ObservedCard associates a public-view instance with its art rectangle. The
// caller supplies card locations; recognition does not locate cards.
type ObservedCard struct {
	Instance string
	Art      ROI
}

// DecisionFrame is a separately captured image and its located visible cards.
type DecisionFrame struct {
	Source string
	Image  image.Image
	Cards  []ObservedCard
}

// verifyFrames checks candidate identities against a supplied public view.
func verifyFrames(matcher *Index, view rules.View, frames [2]DecisionFrame, thresholds Thresholds) error {
	if matcher == nil || frames[0].Source == "" || frames[1].Source == "" || frames[0].Source == frames[1].Source {
		return fmt.Errorf("two distinct frame sources and matcher required")
	}
	expected := make(map[string]int)
	for _, card := range view.Visible {
		if card.Zone == rules.Hand || card.Zone == rules.Board {
			if card.ID == "" || expected[card.ID] != 0 {
				return fmt.Errorf("invalid visible instance identity")
			}
			expected[card.ID] = card.CardID
		}
	}
	for _, frame := range frames {
		seen := make(map[string]bool)
		for _, observed := range frame.Cards {
			id, ok := expected[observed.Instance]
			if !ok || seen[observed.Instance] {
				return fmt.Errorf("unexpected or duplicate visible instance %q", observed.Instance)
			}
			seen[observed.Instance] = true
			feature, err := Normalize(frame.Image, observed.Art)
			if err != nil {
				return fmt.Errorf("instance %s: %w", observed.Instance, err)
			}
			// Search all templates, rather than narrowing to the asserted identity.
			result, err := matcher.Match(feature, nil, thresholds)
			if err != nil {
				return err
			}
			if result.Status != "candidate" || result.CardID != id || result.RunnerDistance < 0 {
				return fmt.Errorf("instance %s identity unresolved or lacks a competing identity", observed.Instance)
			}
		}
		if len(seen) != len(expected) {
			return fmt.Errorf("incomplete visible card observation")
		}
	}
	return nil
}

// CandidateDecision checks two independent frame sources before choosing a legal
// offline action. This does not establish accuracy or authorize client input.
func CandidateDecision(matcher *Index, service *rules.DecisionService, input rules.DecisionInput, frames [2]DecisionFrame, thresholds Thresholds) (rules.Action, error) {
	if service == nil {
		return rules.Action{}, fmt.Errorf("decision service required")
	}
	if err := verifyFrames(matcher, input.View, frames, thresholds); err != nil {
		return rules.Action{}, err
	}
	candidates, err := service.Candidates(input)
	if err != nil {
		return rules.Action{}, err
	}
	input.Candidates = candidates
	return service.Fast(input)
}

// CandidateTurn is an offline turn rehearsal. It never delivers input; callers
// must not use unvalidated candidate matches as proof of live state.
type CandidateTurn struct {
	matcher    *Index
	service    *rules.DecisionService
	tracker    *rules.Tracker
	thresholds Thresholds
	player     int
	pending    *rules.Event
	sources    [2]string
}

func NewCandidateTurn(matcher *Index, engine *rules.Engine, initial rules.State, player int, thresholds Thresholds) (*CandidateTurn, error) {
	if matcher == nil || engine == nil || player < 0 || player > 1 {
		return nil, fmt.Errorf("matcher, engine and valid player required")
	}
	if err := thresholds.Validate(); err != nil {
		return nil, err
	}
	if err := engine.Validate(initial); err != nil {
		return nil, err
	}
	return &CandidateTurn{matcher: matcher, service: rules.NewDecisionService(engine), tracker: rules.NewTracker(engine, initial), thresholds: thresholds, player: player}, nil
}

// Close releases the resident decision worker after the rehearsal is complete.
func (t *CandidateTurn) Close() { t.service.Close() }

// Prepare binds recognition to the tracker's current view and creates one pending
// prediction. Neither a second plan nor a retry is permitted while it is pending.
func (t *CandidateTurn) Prepare(input rules.DecisionInput, frames [2]DecisionFrame) (rules.Event, error) {
	if t.pending != nil {
		return rules.Event{}, fmt.Errorf("unresolved turn")
	}
	view, err := t.tracker.ConfirmedView(t.player)
	if err != nil {
		return rules.Event{}, err
	}
	input.Player, input.View = t.player, view
	action, err := CandidateDecision(t.matcher, t.service, input, frames, t.thresholds)
	if err != nil {
		return rules.Event{}, err
	}
	event, err := t.tracker.Predict(action)
	if err != nil {
		return rules.Event{}, err
	}
	t.pending = &event
	t.sources = [2]string{frames[0].Source, frames[1].Source}
	return event, nil
}

func (t *CandidateTurn) Delivered(sequence int) error {
	return t.tracker.Delivered(sequence)
}

// Confirm requires new image sources and the predicted complete public view.
// Matching card art alone cannot verify scores, turns, or other state fields.
func (t *CandidateTurn) Confirm(view rules.View, frames [2]DecisionFrame) error {
	if t.pending == nil {
		return fmt.Errorf("no pending turn")
	}
	for _, frame := range frames {
		if frame.Source == t.sources[0] || frame.Source == t.sources[1] {
			return fmt.Errorf("confirmation requires fresh frame sources")
		}
	}
	if err := verifyFrames(t.matcher, view, frames, t.thresholds); err != nil {
		return err
	}
	if err := t.tracker.Confirm(t.player, view, frames[0].Source); err != nil {
		return err
	}
	t.pending = nil
	return nil
}
