package observe

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"sync"
	"time"

	"gwentcoachmvp/internal/cardmatch"
	"gwentcoachmvp/internal/matchlog"
	"gwentcoachmvp/internal/rules"
	"gwentcoachmvp/internal/stateestimate"
	"gwentcoachmvp/internal/uiphase"
)

// LoopState binds a calibrated visual state to its independently labelled public view.
var ErrUnstableVisual = errors.New("unknown or unstable visual state")

type LoopState struct {
	View                         rules.View
	HandOrder                    []string
	Eligible, Result             bool
	HandSlots                    []cardmatch.ROI
	CardPoints                   []image.Point
	SelectedPhases, PlacedPhases map[string]string
	TargetPhases                 map[string]string
	AutoEndedPhases              map[string]string
}

type NativeInteraction struct {
	CardPoints                                                  []image.Point
	SelectedPhases, PlacedPhases, TargetPhases, AutoEndedPhases map[string]string
}

type NativeLoopConfig struct {
	EvidenceCalibrationPath, EvidenceCalibrationSHA256                 string
	ResumeJournal                                                      bool
	LegacyScenarioStates                                               bool
	Interactions                                                       map[string]NativeInteraction
	ModelPath, ModelSHA256, StopFile, StateDir, Catalog, MatchID       string
	StartingDeckAudit                                                  string
	Hand                                                               HandScanConfig
	States                                                             map[string]LoopState
	SelectedPhase, PlacedPhase                                         string
	CardPoints                                                         []image.Point
	PlacedArt                                                          map[string]cardmatch.ROI
	EndedArt                                                           map[string]cardmatch.ROI
	RowPoints                                                          [2]image.Point
	PassPoint, EndTurnPoint, StartNeutralPoint                         image.Point
	OrderPoint                                                         image.Point
	OrderTargets                                                       map[string]image.Point
	TargetPoints                                                       map[string]image.Point
	OrderSelectedPhase                                                 string
	GuardProbeStart                                                    image.Point
	MulliganPhase, MulliganConfirmPhase                                string
	MulliganOnTransition                                               bool
	ConfirmationTimeoutMS                                              int
	FinishMulliganPoint, ConfirmMulliganPoint                          image.Point
	ResumeAction                                                       *rules.Action
	ResumeBaseline, ResumeSelectedPhase, ResumePlacedPhase, ResumeCard string
	ResumeOrderPhase, ResumeOrderCard, ResumeOrderTarget               string
	ResumeTargetPhase, ResumeTargetCard, ResumeTargetID                string
	PassWhenNoPlayable                                                 bool
	Prelude                                                            []PreludeTransition
	VerifyOldPrefetch                                                  bool
	PlacedIdentityByPhase                                              bool
	StopAtTargetSelection                                              bool
	StopAfterPlacement                                                 bool
}

type loopFrame struct {
	navFrame
	Stage *uiphase.Event
	Image image.Image
}

type nativeLoopAdapter struct {
	cfg            NativeLoopConfig
	matcher        *cardmatch.Index
	model          *PhaseModel
	evidenceReader interface {
		Read(stateestimate.FrameRef, image.Image) (stateestimate.Evidence, error)
		ReadWithStage(stateestimate.FrameRef, image.Image, uiphase.Evidence) (stateestimate.Evidence, error)
	}
	capture func(context.Context) (loopFrame, error)
	send    func(context.Context, loopFrame, string, image.Point, time.Time) error
	mu      sync.Mutex
	before  SemanticObservation
	last    loopFrame
	state   LoopState
}

func (a *nativeLoopAdapter) observeLocked(ctx context.Context) (SemanticObservation, error) {
	first, err := a.capture(ctx)
	if err != nil {
		return SemanticObservation{}, err
	}
	second, err := a.capture(ctx)
	if err != nil {
		return SemanticObservation{}, err
	}
	if a.evidenceReader != nil {
		var pair [2]stateestimate.Evidence
		for i, frame := range []loopFrame{first, second} {
			ref := stateestimate.FrameRef{ID: frame.Reference, MatchID: a.cfg.MatchID, CapturedAt: frame.Captured, Width: frame.Width, Height: frame.Height, Player: 0}
			var evidence stateestimate.Evidence
			var e error
			if frame.Stage != nil {
				evidence, e = a.evidenceReader.ReadWithStage(ref, frame.Image, frame.Stage.Evidence)
			} else {
				evidence, e = a.evidenceReader.Read(ref, frame.Image)
			}
			if e != nil {
				return SemanticObservation{}, e
			}
			pair[i] = evidence
		}
		o, e := SemanticFromEvidence(pair, time.Now(), 2*time.Second)
		if e != nil {
			return SemanticObservation{}, e
		}
		interaction := a.cfg.Interactions[second.Phase]
		a.last = second
		a.state = LoopState{View: o.View, HandOrder: o.HandOrder, Eligible: o.Eligible, Result: o.Result, CardPoints: interaction.CardPoints, SelectedPhases: interaction.SelectedPhases, PlacedPhases: interaction.PlacedPhases, TargetPhases: interaction.TargetPhases, AutoEndedPhases: interaction.AutoEndedPhases}
		return o, nil
	}
	// Restricted legacy scenario adapter: these labels are not a general estimator.
	state, ok := a.cfg.States[second.Phase]
	for _, frame := range []loopFrame{first, second} {
		if age := time.Since(frame.Captured); frame.Reference == "" || frame.Captured.IsZero() || age < 0 || age > 2*time.Second || frame.Width != 1920 || frame.Height != 1080 {
			return SemanticObservation{}, fmt.Errorf("invalid or stale native frame")
		}
	}
	if !ok || first.Phase != second.Phase || first.Reference == second.Reference {
		return SemanticObservation{}, ErrUnstableVisual
	}
	phase := second.Phase
	if state.Eligible {
		phase = "turn"
	}
	o := SemanticObservation{Source: second.Reference, MatchID: a.cfg.MatchID, Phase: phase, Player: 0, CapturedAt: second.Captured, View: state.View, HandOrder: append([]string(nil), state.HandOrder...), Eligible: state.Eligible, Result: state.Result}
	o.Frames = []matchlog.FrameEvidence{{Source: first.Reference, MatchID: a.cfg.MatchID, CapturedAt: first.Captured}, {Source: second.Reference, MatchID: a.cfg.MatchID, CapturedAt: second.Captured}}
	if o.View.HandCount[0] > 0 {
		handConfig := a.cfg.Hand
		if state.HandSlots != nil {
			handConfig.Slots = state.HandSlots
		}
		if err := VerifyObservedHand(handConfig, a.matcher, [2]HandFrame{{first.Reference, first.Captured, first.Image}, {second.Reference, second.Captured, second.Image}}, o); err != nil {
			if errors.Is(err, cardmatch.ErrDegenerateArt) {
				return SemanticObservation{}, fmt.Errorf("%w: %v", ErrUnstableVisual, err)
			}
			return SemanticObservation{}, err
		}
	} else if len(o.HandOrder) != 0 {
		return SemanticObservation{}, fmt.Errorf("empty hand order inconsistent")
	}
	a.last, a.state = second, state
	return o, nil
}
func (a *nativeLoopAdapter) finishMulligan(ctx context.Context) error {
	if a.cfg.MulliganPhase == "" {
		return nil
	}
	valid := func(p image.Point) bool { return p != (image.Point{}) && p.In(image.Rect(0, 0, 1920, 1080)) }
	if a.cfg.MulliganConfirmPhase == "" || !valid(a.cfg.FinishMulliganPoint) || !valid(a.cfg.ConfirmMulliganPoint) {
		return fmt.Errorf("mulligan transition calibration required")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	o, err := a.observeLocked(ctx)
	if err != nil {
		return err
	}
	if o.Eligible || o.Result || a.last.Phase != a.cfg.MulliganPhase {
		return fmt.Errorf("expected confirmed mulligan state")
	}
	deadline := time.Now().Add(10 * time.Second)
	if err := a.send(ctx, a.last, "click", a.cfg.FinishMulliganPoint, deadline); err != nil {
		return fmt.Errorf("finish mulligan unresolved, do not replay: %w", err)
	}
	frame, err := a.waitPhase(ctx, a.cfg.MulliganConfirmPhase, deadline)
	if err != nil {
		return fmt.Errorf("finish mulligan unresolved, do not replay: %w", err)
	}
	if err := a.send(ctx, frame, "click", a.cfg.ConfirmMulliganPoint, deadline); err != nil {
		return fmt.Errorf("confirm mulligan unresolved, do not replay: %w", err)
	}
	return nil
}

func (a *nativeLoopAdapter) observe(ctx context.Context) (SemanticObservation, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.observeLocked(ctx)
}

func (a *nativeLoopAdapter) observeTarget(ctx context.Context, want PlannedInput, expected *Snapshot) (TargetEvidence, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.evidenceReader == nil {
		return TargetEvidence{}, fmt.Errorf("calibrated target evidence required")
	}
	var pair [2]stateestimate.Evidence
	var frames [2]loopFrame
	for i := range frames {
		frame, err := a.capture(ctx)
		if err != nil {
			return TargetEvidence{}, err
		}
		frames[i] = frame
		ref := stateestimate.FrameRef{ID: frame.Reference, MatchID: a.cfg.MatchID, CapturedAt: frame.Captured, Width: frame.Width, Height: frame.Height, Player: 0}
		if frame.Stage != nil {
			pair[i], err = a.evidenceReader.ReadWithStage(ref, frame.Image, frame.Stage.Evidence)
		} else {
			pair[i], err = a.evidenceReader.Read(ref, frame.Image)
		}
		if err != nil {
			return TargetEvidence{}, err
		}
	}
	if expected != nil {
		o, e := SemanticFromEvidence(pair, time.Now(), 2*time.Second)
		if e != nil {
			return TargetEvidence{}, e
		}
		state, e := semanticState(o)
		if e != nil || !o.Eligible || o.Phase != expected.Phase || o.Source == expected.Source || !bytes.Equal(state, expected.State) {
			return TargetEvidence{}, fmt.Errorf("semantic state changed before input")
		}
	}
	estimate, err := stateestimate.Estimate(pair, time.Now(), 2*time.Second)
	if err != nil {
		return TargetEvidence{}, err
	}
	for _, t := range estimate.Targets {
		if t.Target.Kind == want.TargetKind && t.Target.ID == want.TargetID && t.Target.LayoutID == want.LayoutID {
			a.last = frames[1]
			return TargetEvidence{Kind: t.Target.Kind, ID: t.Target.ID, LayoutID: t.Target.LayoutID, Bounds: t.Target.Bounds, MatchID: a.cfg.MatchID, Sources: [2]string{frames[0].Reference, frames[1].Reference}, Captured: [2]time.Time{frames[0].Captured, frames[1].Captured}}, nil
		}
	}
	return TargetEvidence{}, fmt.Errorf("%w: semantic target not observed", ErrUnstableVisual)
}

func (a *nativeLoopAdapter) execute(ctx context.Context, expected Snapshot, action rules.Action, deadline time.Time) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	o, err := a.observeLocked(ctx)
	if err != nil {
		return err
	}
	b, err := semanticState(o)
	if err != nil {
		return err
	}
	if o.Source == expected.Source || !o.Eligible || o.Phase != expected.Phase || !bytes.Equal(b, expected.State) || !time.Now().Before(deadline) {
		return fmt.Errorf("observation changed before input")
	}
	a.before = o
	if len((ActionPlanner{AllowPassWithCards: a.cfg.PassWhenNoPlayable}).Candidates(o, []rules.Action{action})) != 1 {
		return fmt.Errorf("action unsupported by native planner")
	}
	frame := a.last
	if action.Player != 0 {
		return fmt.Errorf("unsupported player")
	}
	switch action.Kind {
	case "pass":
		if (len(o.HandOrder) != 0 && !a.cfg.PassWhenNoPlayable) || a.cfg.PassPoint == (image.Point{}) {
			return fmt.Errorf("pass unsupported for this state")
		}
		return a.send(ctx, frame, "hold", a.cfg.PassPoint, deadline)
	case "order":
		target, ok := a.cfg.OrderTargets[action.Target]
		if !ok || a.cfg.OrderPoint == (image.Point{}) || target == (image.Point{}) || a.cfg.OrderSelectedPhase == "" {
			return fmt.Errorf("order execution calibration required")
		}
		if err := a.send(ctx, frame, "click", a.cfg.OrderPoint, deadline); err != nil {
			return err
		}
		frame, err = a.waitPhase(ctx, a.cfg.OrderSelectedPhase, deadline)
		if err != nil {
			return err
		}
		return a.send(ctx, frame, "click", target, deadline)
	case "play":
		points := a.state.CardPoints
		if points == nil {
			points = a.cfg.CardPoints
		}
		slot := -1
		for i, id := range o.HandOrder {
			if id == action.Card {
				slot = i
				break
			}
		}
		if slot < 0 || len(points) != len(o.HandOrder) || action.Row < 0 || action.Row > 1 || a.cfg.RowPoints[action.Row] == (image.Point{}) || a.cfg.EndTurnPoint == (image.Point{}) {
			return fmt.Errorf("unsupported play")
		}
		selected, placed := a.state.SelectedPhases[action.Card], a.state.PlacedPhases[action.Card]
		autoEnded := a.state.AutoEndedPhases[action.Card]
		if len(o.HandOrder) == 1 && selected == "" && placed == "" {
			selected, placed = a.cfg.SelectedPhase, a.cfg.PlacedPhase
		}
		if selected == "" || (placed == "" && autoEnded == "" && !a.cfg.StopAtTargetSelection && !a.cfg.StopAfterPlacement) {
			return fmt.Errorf("missing card-specific phase calibration")
		}
		validPoint := func(p image.Point) bool { return p != (image.Point{}) && p.In(image.Rect(0, 0, 1920, 1080)) }
		if !validPoint(points[slot]) || !validPoint(a.cfg.RowPoints[action.Row]) || !validPoint(a.cfg.EndTurnPoint) {
			return fmt.Errorf("invalid play coordinates")
		}
		found := false
		for _, c := range o.View.Visible {
			if c.ID == action.Card && (c.CardID == 202245 || c.CardID == 132403 || c.CardID == 202908 || c.CardID == 202218) && c.Zone == rules.Hand && c.Owner == 0 {
				found = true
			}
			if c.Zone == rules.Board && c.Owner == 1 && action.Card != "" {
				// Wolf Pack needs no target; Keeper's deploy is not modeled here.
				for _, own := range o.View.Visible {
					if own.ID == action.Card && own.CardID != 132403 && own.CardID != 202908 && own.CardID != 202218 {
						return fmt.Errorf("enemy board not empty for this card")
					}
				}
			}
		}
		if !found {
			return fmt.Errorf("unsupported card")
		}
		if action.Target != "" {
			valid := false
			for _, card := range o.View.Visible {
				if card.ID == action.Card && card.CardID == 202908 {
					for _, enemy := range o.View.Visible {
						if enemy.ID == action.Target && enemy.CardID == 200535 && enemy.Owner == 1 && enemy.Zone == rules.Board {
							valid = true
						}
					}
				}
			}
			if !valid || (!a.cfg.StopAtTargetSelection && a.state.TargetPhases[action.Card] == "") || !validPoint(a.cfg.TargetPoints[action.Target]) {
				return fmt.Errorf("unsupported targeted play")
			}
		}
		if err := a.send(ctx, frame, "click", points[slot], deadline); err != nil {
			return err
		}
		frame, err = a.waitPhase(ctx, selected, deadline)
		if err != nil {
			return err
		}
		return a.finishPlay(ctx, frame, action, placed, deadline)
	default:
		return fmt.Errorf("unsupported decision %q", action.Kind)
	}
}

func (a *nativeLoopAdapter) finishPlay(ctx context.Context, frame loopFrame, action rules.Action, placed string, deadline time.Time) error {
	for _, c := range a.before.View.Visible {
		if c.ID == action.Card && c.CardID == 132403 {
			roi, ok := a.cfg.PlacedArt[action.Card]
			if !ok || roi.X < 0 || roi.Y < 0 || roi.Width < 9 || roi.Height < 8 || roi.X > 1920-roi.Width || roi.Y > 1080-roi.Height {
				return fmt.Errorf("valid wolf board-art calibration required")
			}
		}
	}
	if err := a.send(ctx, frame, "click", a.cfg.RowPoints[action.Row], deadline); err != nil {
		return err
	}
	if a.cfg.StopAfterPlacement {
		first, err := a.capture(ctx)
		if err != nil {
			return err
		}
		second, err := a.capture(ctx)
		if err != nil {
			return err
		}
		return fmt.Errorf("placed card requires calibration; inspect %s and %s before resuming", first.Reference, second.Reference)
	}
	if action.Target != "" {
		if a.cfg.StopAtTargetSelection {
			first, err := a.capture(ctx)
			if err != nil {
				return err
			}
			second, err := a.capture(ctx)
			if err != nil {
				return err
			}
			return fmt.Errorf("target selection requires calibration; inspect %s and %s before resuming", first.Reference, second.Reference)
		}
		phase := a.state.TargetPhases[action.Card]
		targetFrame, err := a.waitPhase(ctx, phase, deadline)
		if err != nil {
			return err
		}
		frame = targetFrame
		point := a.cfg.TargetPoints[action.Target]
		if point == (image.Point{}) || !point.In(image.Rect(0, 0, 1920, 1080)) {
			return fmt.Errorf("missing target point")
		}
		if err := a.send(ctx, frame, "click", point, deadline); err != nil {
			return err
		}
	}
	first, second, err := a.waitPlayOutcome(ctx, placed, a.state.AutoEndedPhases[action.Card], deadline)
	if err != nil {
		return err
	}
	if err := a.verifyPlacedWolf(action, first, second, second.Phase); err != nil {
		return err
	}
	if second.Phase == a.state.AutoEndedPhases[action.Card] && second.Phase != "" {
		o, err := a.observeLocked(ctx)
		if err != nil {
			return fmt.Errorf("automatic turn transition unresolved: %w", err)
		}
		if !a.confirm(action, o) || o.View.Turn == a.before.View.Turn {
			return fmt.Errorf("automatic turn transition unresolved")
		}
		return nil
	}
	return a.send(ctx, second, "click", a.cfg.EndTurnPoint, deadline)
}

func (a *nativeLoopAdapter) verifyPlacedWolf(action rules.Action, frame, second loopFrame, phase string) error {
	for _, c := range a.before.View.Visible {
		if c.ID != action.Card || (c.CardID != 132403 && c.CardID != 202908 && c.CardID != 202218) {
			continue
		}
		roi, ok := a.cfg.PlacedArt[action.Card]
		if phase != "" && phase == a.state.AutoEndedPhases[action.Card] {
			roi, ok = a.cfg.EndedArt[action.Card]
		}
		if !ok {
			return fmt.Errorf("board-art calibration required")
		}
		if frame.Reference == second.Reference {
			return fmt.Errorf("fresh board evidence required")
		}
		if a.cfg.PlacedIdentityByPhase {
			if a.model == nil {
				return fmt.Errorf("bound phase model required for board identity")
			}
			if !a.model.RequiresRGBAnchor(phase, image.Rect(roi.X, roi.Y, roi.X+roi.Width, roi.Y+roi.Height), 0.08) {
				return fmt.Errorf("placed wolf art anchor missing from phase variant")
			}
			for _, observed := range []loopFrame{frame, second} {
				if observed.Image == nil {
					return fmt.Errorf("placed frame missing image")
				}
				name, _ := a.model.Recognize(observed.Image)
				if name != phase {
					return fmt.Errorf("placed wolf phase identity unresolved")
				}
			}
			return nil
		}
		for _, observed := range []loopFrame{frame, second} {
			feature, err := cardmatch.Normalize(observed.Image, roi)
			if err != nil {
				return fmt.Errorf("placed card art: %w", err)
			}
			match, err := a.matcher.Match(feature, nil, cardmatch.Thresholds{MaxDistance: a.cfg.Hand.MaxDistance, MinGap: a.cfg.Hand.MinGap})
			if err != nil {
				return err
			}
			if match.Status != "candidate" || match.CardID != c.CardID || match.RunnerDistance < 0 {
				return fmt.Errorf("placed wolf identity unresolved")
			}
		}
	}
	return nil
}

func (a *nativeLoopAdapter) recoveryAction(input rules.DecisionInput, evaluator ActionEvaluator) (rules.Action, error) {
	if a.cfg.ResumeAction == nil {
		return rules.Action{}, fmt.Errorf("recovery requires the recorded action; do not reselect")
	}
	action := *a.cfg.ResumeAction
	if err := evaluator.ValidateAction(input, action); err != nil {
		return rules.Action{}, err
	}
	if len(filterNativeCandidates(a.before, []rules.Action{action})) != 1 {
		return rules.Action{}, fmt.Errorf("recorded recovery action is not executable")
	}
	return action, nil
}

func (a *nativeLoopAdapter) resumeSelected(ctx context.Context, service ActionEvaluator) error {
	if a.cfg.ResumeCard == "" {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	state, ok := a.cfg.States[a.cfg.ResumeBaseline]
	if !ok || !state.Eligible || (a.cfg.ResumeSelectedPhase == "") == (a.cfg.ResumePlacedPhase == "") {
		return fmt.Errorf("resume requires baseline and exactly one intermediate phase")
	}
	phase := a.cfg.ResumeSelectedPhase
	if a.cfg.ResumePlacedPhase != "" {
		phase = a.cfg.ResumePlacedPhase
	}
	deadline := time.Now().Add(10 * time.Second)
	var frame, first loopFrame
	for i := 0; i < 2; i++ {
		fresh, err := a.capture(ctx)
		if err != nil {
			return err
		}
		if fresh.Phase != phase || fresh.Reference == "" || fresh.Reference == frame.Reference || time.Since(fresh.Captured) < 0 || time.Since(fresh.Captured) > 2*time.Second {
			return fmt.Errorf("selected action no longer independently observed")
		}
		if i == 0 {
			first = fresh
		}
		frame = fresh
	}
	input := rules.DecisionInput{MatchID: a.cfg.MatchID, Phase: "turn", Player: 0, View: state.View, HandOrder: state.HandOrder, Deadline: deadline}
	var err error
	input.Candidates, err = service.Candidates(input)
	if err != nil {
		return err
	}
	a.before = SemanticObservation{MatchID: a.cfg.MatchID, Player: 0, View: state.View, HandOrder: state.HandOrder, Eligible: true}
	a.state = state
	input.Candidates = filterNativeCandidates(a.before, input.Candidates)
	action, err := a.recoveryAction(input, service)
	if err != nil || action.Card != a.cfg.ResumeCard {
		return fmt.Errorf("selected action differs from legal decision")
	}
	validPoint := func(p image.Point) bool { return p != (image.Point{}) && p.In(image.Rect(0, 0, 1920, 1080)) }
	if action.Row < 0 || action.Row > 1 || !validPoint(a.cfg.RowPoints[action.Row]) || !validPoint(a.cfg.EndTurnPoint) {
		return fmt.Errorf("missing action coordinates")
	}
	placed := state.PlacedPhases[action.Card]
	if placed == "" {
		return fmt.Errorf("missing placed phase")
	}
	if a.cfg.ResumePlacedPhase != "" {
		if action.Card != a.cfg.ResumeCard || action.Kind != "play" {
			return fmt.Errorf("invalid placed recovery action")
		}
		cardID := 0
		for _, card := range state.View.Visible {
			if card.ID == action.Card && card.Zone == rules.Hand {
				cardID = card.CardID
			}
		}
		if (cardID != 132403 || action.Target != "") && (cardID != 202908 || action.Target == "") && (cardID != 202218 || action.Target != "") {
			return fmt.Errorf("placed recovery requires a verified wolf, mage or scholar")
		}
		if err := a.verifyPlacedWolf(action, first, frame, a.cfg.ResumePlacedPhase); err != nil {
			return err
		}
		if cardID == 202908 || cardID == 202218 {
			result, err := a.observeLocked(ctx)
			if err != nil || !a.confirm(action, result) {
				return fmt.Errorf("mage result unresolved before end turn")
			}
			frame = a.last
		}
		if err := a.send(ctx, frame, "click", a.cfg.EndTurnPoint, deadline); err != nil {
			return fmt.Errorf("resumed end turn unresolved, do not replay: %w", err)
		}
	} else if err := a.finishPlay(ctx, frame, action, placed, deadline); err != nil {
		return fmt.Errorf("resumed action unresolved, do not replay: %w", err)
	}
	if err := a.waitConfirmation(ctx, action, time.Now().Add(10*time.Second)); err != nil {
		return fmt.Errorf("resumed action confirmation unresolved: %w", err)
	}
	return nil
}

func (a *nativeLoopAdapter) resumeOrderSelected(ctx context.Context, service ActionEvaluator) error {
	if a.cfg.ResumeOrderCard == "" {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	state, ok := a.cfg.States[a.cfg.ResumeBaseline]
	if !ok || !state.Eligible || a.cfg.ResumeOrderPhase == "" || a.cfg.ResumeOrderTarget == "" {
		return fmt.Errorf("order recovery requires baseline and selected target")
	}
	deadline := time.Now().Add(10 * time.Second)
	var frame loopFrame
	for i := 0; i < 2; i++ {
		fresh, err := a.capture(ctx)
		if err != nil {
			return err
		}
		if fresh.Phase != a.cfg.ResumeOrderPhase || fresh.Reference == "" || fresh.Reference == frame.Reference || time.Since(fresh.Captured) < 0 || time.Since(fresh.Captured) > 2*time.Second {
			return fmt.Errorf("order target selection no longer independently observed")
		}
		frame = fresh
	}
	input := rules.DecisionInput{MatchID: a.cfg.MatchID, Phase: "turn", Player: 0, View: state.View, HandOrder: state.HandOrder, Deadline: deadline}
	var err error
	input.Candidates, err = service.Candidates(input)
	if err != nil {
		return err
	}
	a.before = SemanticObservation{MatchID: a.cfg.MatchID, Player: 0, View: state.View, HandOrder: state.HandOrder, Eligible: true}
	input.Candidates = filterNativeCandidates(a.before, input.Candidates)
	action, err := a.recoveryAction(input, service)
	if err != nil || action.Kind != "order" || action.Card != a.cfg.ResumeOrderCard || action.Target != a.cfg.ResumeOrderTarget {
		return fmt.Errorf("selected order differs from legal decision")
	}
	target, ok := a.cfg.OrderTargets[action.Target]
	if !ok || !target.In(image.Rect(1, 1, 1920, 1080)) {
		return fmt.Errorf("missing selected order target")
	}
	if err := a.send(ctx, frame, "click", target, deadline); err != nil {
		return fmt.Errorf("order target unresolved, do not replay: %w", err)
	}
	if err := a.waitConfirmation(ctx, action, time.Now().Add(10*time.Second)); err != nil {
		return fmt.Errorf("order confirmation unresolved: %w", err)
	}
	return nil
}

func (a *nativeLoopAdapter) resumeMageTarget(ctx context.Context, service ActionEvaluator) error {
	if a.cfg.ResumeTargetCard == "" {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	state, ok := a.cfg.States[a.cfg.ResumeBaseline]
	if !ok || !state.Eligible || a.cfg.ResumeTargetPhase == "" || a.cfg.ResumeTargetID == "" {
		return fmt.Errorf("mage recovery requires baseline and target phase")
	}
	deadline := time.Now().Add(10 * time.Second)
	var frame loopFrame
	for i := 0; i < 2; i++ {
		fresh, err := a.capture(ctx)
		if err != nil {
			return err
		}
		if fresh.Phase != a.cfg.ResumeTargetPhase || fresh.Reference == "" || fresh.Reference == frame.Reference || time.Since(fresh.Captured) < 0 || time.Since(fresh.Captured) > 2*time.Second {
			return fmt.Errorf("mage target selection no longer independently observed")
		}
		frame = fresh
	}
	input := rules.DecisionInput{MatchID: a.cfg.MatchID, Phase: "turn", Player: 0, View: state.View, HandOrder: state.HandOrder, Deadline: deadline}
	var err error
	input.Candidates, err = service.Candidates(input)
	if err != nil {
		return err
	}
	a.before = SemanticObservation{MatchID: a.cfg.MatchID, Player: 0, View: state.View, HandOrder: state.HandOrder, Eligible: true}
	a.state = state
	input.Candidates = filterNativeCandidates(a.before, input.Candidates)
	action, err := a.recoveryAction(input, service)
	if err != nil || action.Kind != "play" || action.Card != a.cfg.ResumeTargetCard || action.Target != a.cfg.ResumeTargetID {
		return fmt.Errorf("selected mage differs from legal decision")
	}
	verified := false
	for _, card := range state.View.Visible {
		if card.ID == action.Card && card.CardID == 202908 && card.Owner == 0 && card.Zone == rules.Hand {
			verified = true
		}
	}
	if !verified {
		return fmt.Errorf("mage recovery requires verified Mage Assassin")
	}
	point, ok := a.cfg.TargetPoints[action.Target]
	if !ok || !point.In(image.Rect(1, 1, 1920, 1080)) {
		return fmt.Errorf("missing mage target point")
	}
	if err := a.send(ctx, frame, "click", point, deadline); err != nil {
		return fmt.Errorf("mage target unresolved, do not replay: %w", err)
	}
	placed := state.PlacedPhases[action.Card]
	if placed == "" {
		first, err := a.capture(ctx)
		if err != nil {
			return err
		}
		second, err := a.capture(ctx)
		if err != nil {
			return err
		}
		return fmt.Errorf("mage result requires calibration; inspect %s and %s before resuming", first.Reference, second.Reference)
	}
	first, err := a.waitPhase(ctx, placed, deadline)
	if err != nil {
		return err
	}
	second, err := a.waitPhase(ctx, placed, deadline)
	if err != nil {
		return err
	}
	if err := a.verifyPlacedWolf(action, first, second, placed); err != nil {
		return err
	}
	beforeEnd, err := a.observeLocked(ctx)
	if err != nil {
		return fmt.Errorf("mage result unresolved before end turn: %w", err)
	}
	if !a.confirm(action, beforeEnd) {
		return fmt.Errorf("mage result unresolved before end turn")
	}
	if err := a.send(ctx, a.last, "click", a.cfg.EndTurnPoint, deadline); err != nil {
		return fmt.Errorf("mage end turn unresolved, do not replay: %w", err)
	}
	if err := a.waitConfirmation(ctx, action, time.Now().Add(10*time.Second)); err != nil {
		return fmt.Errorf("mage confirmation unresolved: %w", err)
	}
	return nil
}

func (a *nativeLoopAdapter) waitConfirmation(ctx context.Context, action rules.Action, deadline time.Time) error {
	for time.Now().Before(deadline) {
		o, err := a.observeLocked(ctx)
		if err == nil && a.confirm(action, o) {
			return nil
		}
		if err != nil && !errors.Is(err, ErrUnstableVisual) {
			return err
		}
		select {
		case <-time.After(100 * time.Millisecond):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return fmt.Errorf("confirmation timed out; do not replay")
}

func (a *nativeLoopAdapter) waitPlayOutcome(ctx context.Context, placed, ended string, deadline time.Time) (loopFrame, loopFrame, error) {
	var first loopFrame
	for time.Now().Before(deadline) {
		frame, err := a.capture(ctx)
		if err != nil {
			return loopFrame{}, loopFrame{}, err
		}
		if frame.Phase != "" && (frame.Phase == placed || frame.Phase == ended) {
			if frame.Phase == first.Phase && frame.Reference != "" && frame.Reference != first.Reference {
				return first, frame, nil
			}
			first = frame
		} else {
			first = loopFrame{}
		}
		select {
		case <-time.After(100 * time.Millisecond):
		case <-ctx.Done():
			return loopFrame{}, loopFrame{}, ctx.Err()
		}
	}
	return loopFrame{}, loopFrame{}, fmt.Errorf("placed or automatically ended phase not observed before deadline")
}

func (a *nativeLoopAdapter) waitPhase(ctx context.Context, phase string, deadline time.Time) (loopFrame, error) {
	if phase == "" {
		return loopFrame{}, fmt.Errorf("missing intermediate phase")
	}
	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			return loopFrame{}, err
		}
		f, err := a.capture(ctx)
		if err != nil {
			return f, err
		}
		if f.Phase == phase {
			return f, nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return loopFrame{}, fmt.Errorf("intermediate phase %s not observed before deadline", phase)
}

func (p ActionPlanner) Candidates(o SemanticObservation, actions []rules.Action) []rules.Action {
	return filterNativeCandidatesWithPass(o, actions, p.AllowPassWithCards)
}

func filterNativeCandidates(o SemanticObservation, actions []rules.Action) []rules.Action {
	return (ActionPlanner{}).Candidates(o, actions)
}
func filterNativeCandidatesWithPass(o SemanticObservation, actions []rules.Action, allowPass bool) []rules.Action {
	enemy := false
	for _, c := range o.View.Visible {
		if c.Zone == rules.Board && c.Owner == 1 {
			enemy = true
		}
	}
	var filtered []rules.Action
	for _, action := range actions {
		if action.Player != 0 {
			continue
		}
		if action.Kind == "pass" && (len(o.HandOrder) == 0 || allowPass) {
			filtered = append(filtered, action)
			continue
		}
		if action.Kind == "order" && action.Card != "" && action.Target != "" {
			for _, card := range o.View.Visible {
				if card.ID == action.Card && card.CardID == 202140 && card.Owner == 0 && card.Zone == rules.Board {
					filtered = append(filtered, action)
				}
			}
			continue
		}
		if action.Kind == "play" && action.Target != "" {
			mage, target := false, false
			for _, card := range o.View.Visible {
				if card.ID == action.Card && card.Owner == 0 && card.Zone == rules.Hand && card.CardID == 202908 {
					mage = true
				}
				if card.ID == action.Target && card.Owner == 1 && card.Zone == rules.Board && card.CardID == 200535 {
					target = true
				}
			}
			if mage && target {
				filtered = append(filtered, action)
			}
			continue
		}
		if action.Kind != "play" {
			continue
		}
		for _, c := range o.View.Visible {
			if c.ID == action.Card && c.Owner == 0 && c.Zone == rules.Hand && (c.CardID == 132403 || (c.CardID == 202245 && !enemy) || (c.CardID == 202218 && o.View.StartingDeckKnown)) {
				filtered = append(filtered, action)
				break
			}
		}
	}
	return filtered
}

func (a *nativeLoopAdapter) confirm(action rules.Action, o SemanticObservation) bool {
	return verifyNativeAction(a.before, action, o)
}

func verifyNativeAction(original SemanticObservation, action rules.Action, o SemanticObservation) bool {
	before := original.View
	if o.Player != 0 {
		return false
	}
	switch action.Kind {
	case "pass":
		return !before.Passed[0] && (o.View.Passed[0] || o.Result || (o.Phase == "mulligan" && o.View.Round == before.Round+1))
	case "order":
		beforePower, foundBefore := 0, false
		for _, c := range before.Visible {
			if c.ID == action.Target && c.Owner == 0 && c.Zone == rules.Board {
				beforePower, foundBefore = c.Power, true
			}
		}
		present, boosted := false, false
		for _, c := range o.View.Visible {
			if c.ID == action.Card {
				present = true
			}
			if c.ID == action.Target && c.Owner == 0 && c.Zone == rules.Board && c.Power == beforePower+5 {
				boosted = true
			}
		}
		return foundBefore && !present && boosted && o.View.HandCount[0] == before.HandCount[0] && o.View.Score[0] == before.Score[0]+5 && o.View.Turn == before.Turn
	case "play":
		power, cardID, allies := 0, 0, 0
		for _, c := range before.Visible {
			if c.ID == action.Card && c.Owner == 0 && c.Zone == rules.Hand {
				power, cardID = c.Power, c.CardID
			}
			if c.Owner == 0 && c.Zone == rules.Board && c.Row == action.Row && c.CardID != 202140 {
				allies++
			}
		}
		if cardID == 132403 {
			power += allies
		}
		if cardID == 202218 {
			if !before.StartingDeckKnown || !o.View.StartingDeckKnown || before.StartingHighProvision != o.View.StartingHighProvision || o.View.Score[1] != before.Score[1] {
				return false
			}
			power += before.StartingHighProvision
		}
		if cardID == 202908 {
			var oldEnemy, newEnemy int
			foundOld, foundNew := false, false
			for _, c := range before.Visible {
				if c.ID == action.Target && c.CardID == 200535 && c.Owner == 1 && c.Zone == rules.Board && c.Armor == 0 {
					oldEnemy, foundOld = c.Power, true
				}
			}
			for _, c := range o.View.Visible {
				if c.ID == action.Target && c.CardID == 200535 && c.Owner == 1 && c.Zone == rules.Board && c.Armor == 0 {
					newEnemy, foundNew = c.Power, true
				}
			}
			if !foundOld || !foundNew || oldEnemy <= 2 || newEnemy != oldEnemy-2 || o.View.Score[1] != before.Score[1]-2 {
				return false
			}
		}
		if (cardID != 202245 && cardID != 132403 && cardID != 202908 && cardID != 202218) || o.View.HandCount[0] != before.HandCount[0]-1 || o.View.Score[0] != before.Score[0]+power {
			return false
		}
		for _, c := range o.View.Visible {
			if c.ID == action.Card && c.CardID == cardID && c.Owner == 0 && c.Zone == rules.Board && c.Row == action.Row {
				return true
			}
		}
	}
	return false
}
