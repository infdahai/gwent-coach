package observe

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"gwentcoachmvp/internal/matchlog"
	"gwentcoachmvp/internal/rules"
	"gwentcoachmvp/internal/stateestimate"
)

// SemanticObservation must come from a state estimator, not a phase label.
// Eligible is true only when the estimator has confirmed the complete view and
// that the local player can act. Result is an independently observed terminal.
type SemanticObservation struct {
	Source, MatchID, Phase  string
	CalibrationID, StateKey string
	Player                  int
	CapturedAt              time.Time
	Frames                  []matchlog.FrameEvidence
	View                    rules.View
	HandOrder               []string
	Eligible, Result        bool
	PartialResult           *stateestimate.OutcomeFacts `json:",omitempty"`
}

// LocalLoop connects a persistent observation producer to one decision service.
// Execute must revalidate the observation and version immediately before input;
// a returned error is uncertain delivery and terminates the loop without retry.
// Confirm must compare an action-specific postcondition with fresh observation.
func semanticState(o SemanticObservation) ([]byte, error) {
	var view *rules.View
	if o.PartialResult == nil {
		view = &o.View
	}
	return json.Marshal(struct {
		MatchID                 string
		CalibrationID, StateKey string
		Player                  int
		View                    *rules.View `json:",omitempty"`
		HandOrder               []string
		Eligible, Result        bool
		PartialResult           *stateestimate.OutcomeFacts `json:",omitempty"`
	}{o.MatchID, o.CalibrationID, o.StateKey, o.Player, view, o.HandOrder, o.Eligible, o.Result, o.PartialResult})
}

// GuardedExecution connects fresh semantic observation to a concrete input
// adapter. Delivery errors remain unresolved; the caller must never retry.
func GuardedExecution(observe func(context.Context) (SemanticObservation, error), send func(context.Context, SemanticObservation, rules.Action) error) func(context.Context, Snapshot, rules.Action, time.Time) error {
	return func(ctx context.Context, expected Snapshot, action rules.Action, deadline time.Time) error {
		ctx, cancel := context.WithDeadline(ctx, deadline)
		defer cancel()
		o, err := observe(ctx)
		if err != nil {
			return err
		}
		age := time.Since(o.CapturedAt)
		if o.Source == "" || o.Source == expected.Source || age < 0 || age > 2*time.Second || o.CapturedAt.IsZero() || !o.Eligible || o.Result || o.Phase != expected.Phase {
			return fmt.Errorf("fresh eligible observation required")
		}
		state, err := semanticState(o)
		if err != nil {
			return err
		}
		if !bytes.Equal(state, expected.State) {
			return fmt.Errorf("semantic state changed before input")
		}
		if deadline.IsZero() || !time.Now().Before(deadline) {
			return fmt.Errorf("decision deadline expired before input")
		}
		return send(ctx, o, action)
	}
}

// DecisionPolicy is the resident strategy API. Calls to Start, Poll and
// Invalidate are serialized by the loop; candidates come from the rules layer.
type DecisionPolicy interface {
	Start(rules.DecisionInput, uint64) error
	Poll(rules.DecisionInput) (rules.Action, error)
	Invalidate()
}

type LocalLoop struct {
	Observe              func(context.Context) (SemanticObservation, error)
	Execute              func(context.Context, Snapshot, rules.Action, time.Time) error
	Confirm              func(rules.Action, SemanticObservation) bool
	Decisions            ActionEvaluator
	Policy               DecisionPolicy
	Journal              *ActionJournal
	DecisionBudget       time.Duration
	ConfirmationBudget   time.Duration
	OnResult             func(SemanticObservation)
	OnDecision           func(rules.Action, time.Duration)
	OnDiscardPredelivery func(SemanticObservation, time.Duration)
	OnRoundStart         func(context.Context, SemanticObservation) error
	FilterCandidates     func(SemanticObservation, []rules.Action) []rules.Action
	AllowPassWithCards   bool
	Plan                 func(SemanticObservation, rules.Action, time.Time) (ActionPlan, error)
	ObserveTarget        func(context.Context, PlannedInput, *Snapshot) (TargetEvidence, error)
	ExecutePlanned       func(context.Context, PlannedInput, TargetEvidence, time.Time) error
	ObserveAutoEnd       func(context.Context, rules.Action, time.Time) (SemanticObservation, bool, error)
}

func beforeDelivery(captured, delivered time.Time) bool {
	return !captured.After(delivered)
}

func (l LocalLoop) drivePlan(ctx context.Context, p *PendingPlan, original Snapshot, id string) error {
	ctx, cancel := context.WithDeadline(ctx, p.Plan.Deadline)
	defer cancel()
	for p.Next < len(p.Plan.Inputs) {
		if !time.Now().Before(p.Plan.Deadline) {
			return fmt.Errorf("action deadline expired")
		}
		step := p.Plan.Inputs[p.Next]
		if p.Plan.AutoEnd && p.Next == len(p.Plan.Inputs)-1 {
			if l.ObserveAutoEnd == nil {
				return fmt.Errorf("automatic end observation unavailable")
			}
			o, ended, err := l.ObserveAutoEnd(ctx, p.Plan.Action, p.After)
			if errors.Is(err, ErrUnstableVisual) {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(50 * time.Millisecond):
					continue
				}
			}
			if err != nil {
				return err
			}
			if ended {
				return l.Journal.MarkAutoEnd(id, o)
			}
			if step.Kind == "" {
				return fmt.Errorf("automatic end not confirmed and no calibrated end-turn target")
			}
		}
		var expected *Snapshot
		if p.Next == 0 {
			expected = &original
		}
		evidence, err := l.ObserveTarget(ctx, step, expected)
		if errors.Is(err, ErrUnstableVisual) {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(50 * time.Millisecond):
				continue
			}
		}
		if err != nil {
			return err
		}
		if _, err = p.NextInput(time.Now(), evidence); err != nil {
			return err
		}
		if err = l.ExecutePlanned(ctx, step, evidence, p.Plan.Deadline); err != nil {
			return err
		}
		recorded := l.Journal.Pending()
		if recorded == nil || len(recorded.Steps) == 0 || recorded.Steps[len(recorded.Steps)-1].Receipt == nil {
			return fmt.Errorf("input receipt missing; do not replay")
		}
		if err = p.RecordReceipt(*recorded.Steps[len(recorded.Steps)-1].Receipt); err != nil {
			return err
		}
	}
	return nil
}

func (l LocalLoop) Run(ctx context.Context) (runErr error) {
	if l.Journal != nil {
		defer func() {
			if runErr != nil {
				runErr = errors.Join(runErr, l.Journal.Unresolved(runErr))
			}
		}()
	}
	if l.Observe == nil || (l.Execute == nil && (l.Plan == nil || l.ObserveTarget == nil || l.ExecutePlanned == nil || l.Journal == nil)) || l.Confirm == nil || l.Decisions == nil || l.DecisionBudget <= 0 || l.ConfirmationBudget <= 0 {
		return fmt.Errorf("observation, execution, confirmation, decision service and positive budgets required")
	}
	if l.Policy != nil {
		defer l.Policy.Invalidate()
	}
	type result struct {
		observation SemanticObservation
		err         error
	}
	frames := make(chan result)
	worker, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		defer close(frames)
		for {
			o, err := l.Observe(worker)
			select {
			case frames <- result{o, err}:
			case <-worker.Done():
				return
			}
			if err != nil && !errors.Is(err, ErrUnstableVisual) {
				return
			}
		}
	}()
	var life Lifecycle
	var pending *rules.Action
	var actionID, pendingMatch string
	var active *rules.DecisionInput
	var snapshot Snapshot
	var currentObservation SemanticObservation
	var decisionTimer, confirmationTimer *time.Timer
	var decisionC, confirmationC, pollC <-chan time.Time
	var confirmationDeadline, deliveredAt, observedAt, discardBefore, transitionUntil time.Time
	lastMulliganRound := 0
	poller := time.NewTicker(10 * time.Millisecond)
	defer poller.Stop()
	defer func() {
		if decisionTimer != nil {
			decisionTimer.Stop()
		}
		if confirmationTimer != nil {
			confirmationTimer.Stop()
		}
	}()
	if l.Journal != nil && l.Journal.original.Version > 0 {
		recovery := l.Journal.Pending()
		var intent *Intent
		if recovery != nil {
			if recovery.Domain != "gameplay" {
				return fmt.Errorf("navigation recovery belongs to the session controller")
			}
			if !recovery.DeliveryComplete {
				if l.Plan == nil {
					return fmt.Errorf("persisted action has unresolved steps; inspect original receipts, do not reselect")
				}
			}
			var action rules.Action
			var restoredPlan ActionPlan
			if l.Plan != nil {
				if err := json.Unmarshal(recovery.Action, &restoredPlan); err != nil {
					return err
				}
				action = restoredPlan.Action
			} else if err := json.Unmarshal(recovery.Action, &action); err != nil {
				return err
			}
			var original SemanticObservation
			if err := json.Unmarshal(recovery.Snapshot.State, &original); err != nil || original.MatchID == "" {
				return fmt.Errorf("persisted action missing original observation")
			}
			pending = &action
			actionID = recovery.ID
			pendingMatch = original.MatchID
			intent = &Intent{ID: actionID, Version: recovery.Snapshot.Version, Action: recovery.Action}
			if !recovery.DeliveryComplete {
				if len(recovery.Steps) > len(restoredPlan.Inputs) {
					return fmt.Errorf("recorded steps exceed original plan")
				}
				p := PendingPlan{Plan: restoredPlan, MatchID: original.MatchID}
				for i, step := range recovery.Steps {
					want := restoredPlan.Inputs[i]
					if step.Input.Kind != want.Kind || step.Input.X != want.Point.X || step.Input.Y != want.Point.Y || step.Receipt == nil {
						return fmt.Errorf("recorded input differs from plan or lacks receipt")
					}
					if err := p.RecordReceipt(*step.Receipt); err != nil {
						return err
					}
				}
				if recovery.AutoEnded {
					if !restoredPlan.AutoEnd || p.Next != len(restoredPlan.Inputs)-1 {
						return fmt.Errorf("recorded auto-end differs from original plan")
					}
				} else if err := l.drivePlan(ctx, &p, recovery.Snapshot, actionID); err != nil {
					return fmt.Errorf("recovered action unresolved: %w", err)
				}
				if err := l.Journal.MarkDelivered(actionID); err != nil {
					return err
				}
			}
			var err error
			deliveredAt, err = l.Journal.DeliveredAt(actionID)
			if err != nil {
				return err
			}
			confirmationDeadline = time.Now().Add(l.ConfirmationBudget)
			if l.Plan != nil && restoredPlan.Deadline.Before(confirmationDeadline) {
				confirmationDeadline = restoredPlan.Deadline
			}
			if !time.Now().Before(confirmationDeadline) {
				return fmt.Errorf("action confirmation deadline expired")
			}
			confirmationTimer = time.NewTimer(time.Until(confirmationDeadline))
			confirmationC = confirmationTimer.C
		}
		if err := life.Restore(l.Journal.original, intent); err != nil {
			return err
		}
	}
	for {
		select {
		case <-decisionC:
			return fmt.Errorf("decision deadline expired while waiting for observation or policy")
		case <-confirmationC:
			return fmt.Errorf("action confirmation timed out; outcome unresolved, do not replay")
		case <-ctx.Done():
			return ctx.Err()
		case frame, ok := <-frames:
			if pending != nil && !time.Now().Before(confirmationDeadline) {
				return fmt.Errorf("action confirmation timed out; outcome unresolved, do not replay")
			}
			if !ok {
				return fmt.Errorf("observation ended")
			}
			if frame.err != nil {
				if errors.Is(frame.err, ErrUnstableVisual) && (pending != nil || time.Now().Before(transitionUntil)) {
					continue
				}
				return frame.err
			}
			o := frame.observation
			if o.PartialResult != nil {
				if !o.Result || o.Eligible || o.CalibrationID == "" || o.Player < 0 || o.Player > 1 || !o.PartialResult.Finished || o.PartialResult.Winner < -1 || o.PartialResult.Winner > 1 {
					return fmt.Errorf("invalid partial result")
				}
				if err := matchlog.ValidateConfirmation(o.MatchID, o.Source, o.CapturedAt, o.Frames, time.Unix(0, 0)); err != nil {
					return err
				}
			}
			currentObservation = o
			if o.Source == "" || o.MatchID == "" || o.CapturedAt.IsZero() {
				return fmt.Errorf("missing or stale semantic observation")
			}
			if !discardBefore.IsZero() && !o.CapturedAt.After(discardBefore) {
				continue
			}
			// An observation captured before delivery is not a confirmation;
			// discard it even if the action took long enough to make it stale.
			if pending != nil && beforeDelivery(o.CapturedAt, deliveredAt) {
				if l.OnDiscardPredelivery != nil {
					l.OnDiscardPredelivery(o, time.Since(o.CapturedAt))
				}
				continue
			}
			if time.Since(o.CapturedAt) < 0 || time.Since(o.CapturedAt) > 2*time.Second || (o.PartialResult != nil && time.Since(o.Frames[0].CapturedAt) > 2*time.Second) {
				return fmt.Errorf("missing or stale semantic observation")
			}
			state, err := semanticState(o)
			if err != nil {
				return err
			}
			var changed bool
			snapshot, changed, err = life.Publish(o.Source, o.Phase, state)
			if err != nil {
				return err
			}
			if changed {
				observedAt = o.CapturedAt
				if decisionTimer != nil {
					decisionTimer.Stop()
					decisionC = nil
				}
				pollC = nil
				if l.Policy != nil {
					l.Policy.Invalidate()
				}
				active = nil
			}
			if pending != nil {
				if o.MatchID != pendingMatch {
					return fmt.Errorf("match changed with an unresolved action")
				}
				// A terminal outcome alone cannot establish an action postcondition.
				if o.PartialResult != nil || !changed || !l.Confirm(*pending, o) {
					continue
				}
				if l.Journal != nil {
					if err := l.Journal.Confirm(actionID, snapshot, o); err != nil {
						return err
					}
				}
				if err := life.Confirm(actionID, snapshot.Version, true); err != nil {
					return err
				}
				confirmationTimer.Stop()
				confirmationC = nil
				pending = nil
			}
			if !transitionUntil.IsZero() && o.Phase != "mulligan" {
				transitionUntil = time.Time{}
			}
			if l.OnRoundStart != nil && o.PartialResult == nil && !o.Eligible && o.Phase == "mulligan" && o.View.Round > lastMulliganRound {
				lastMulliganRound = o.View.Round
				if err := l.OnRoundStart(ctx, o); err != nil {
					return fmt.Errorf("mulligan transition unresolved, do not replay: %w", err)
				}
				if l.Journal != nil && l.Journal.original.Version > snapshot.Version {
					life = Lifecycle{}
					if err := life.Restore(l.Journal.original, nil); err != nil {
						return err
					}
				}
				discardBefore = time.Now()
				transitionUntil = discardBefore.Add(30 * time.Second)
				continue
			}
			if o.Result {
				if l.Journal != nil {
					if err := l.Journal.Result(snapshot, o); err != nil {
						return err
					}
				}
				if l.OnResult != nil {
					l.OnResult(o)
				}
				return nil
			}
			if !o.Eligible {
				continue
			}
			if changed || active == nil {
				observedAt = o.CapturedAt
				remaining := l.DecisionBudget - time.Since(o.CapturedAt)
				if remaining <= 0 {
					return fmt.Errorf("decision deadline expired after observation")
				}
				input := rules.DecisionInput{MatchID: o.MatchID, Phase: o.Phase, Player: o.Player, View: o.View, HandOrder: append([]string(nil), o.HandOrder...), Deadline: time.Now().Add(remaining), AllowPassWithCards: l.AllowPassWithCards}
				input.Candidates, err = l.Decisions.Candidates(input)
				if err != nil {
					return fmt.Errorf("legal candidates: %w", err)
				}
				if l.FilterCandidates != nil {
					allowed := make(map[rules.Action]bool, len(input.Candidates))
					for _, a := range input.Candidates {
						allowed[a] = true
					}
					filtered := l.FilterCandidates(o, append([]rules.Action(nil), input.Candidates...))
					seen := map[rules.Action]bool{}
					for _, a := range filtered {
						if !allowed[a] || seen[a] {
							return fmt.Errorf("candidate filter must return a unique rules-candidate subset")
						}
						seen[a] = true
					}
					input.Candidates = filtered
				}
				active = &input
				decisionTimer = time.NewTimer(time.Until(input.Deadline))
				decisionC = decisionTimer.C
				if l.Policy != nil {
					if err := l.Policy.Start(input, snapshot.Version); err != nil {
						return fmt.Errorf("start decision: %w", err)
					}
					pollC = poller.C
				}
			}
		case <-pollC:
		}
		if active == nil || pending != nil {
			continue
		}
		var action rules.Action
		var err error
		if l.Policy == nil {
			action, err = chooseFallback(l.Decisions, *active)
		} else {
			action, err = l.Policy.Poll(*active)
		}
		if err == rules.ErrNoDecision && l.Policy != nil {
			if time.Now().Before(active.Deadline) {
				continue
			}
			return fmt.Errorf("decision deadline expired while waiting for observation or policy")
		}
		if err != nil {
			return fmt.Errorf("decision: %w", err)
		}
		valid := false
		for _, candidate := range active.Candidates {
			if candidate == action {
				valid = true
				break
			}
		}
		if !valid || !time.Now().Before(active.Deadline) {
			return fmt.Errorf("stale or illegal policy action")
		}
		if err := l.Decisions.ValidateAction(*active, action); err != nil {
			return fmt.Errorf("action failed final rules validation: %w", err)
		}
		if !time.Now().Before(active.Deadline) {
			return fmt.Errorf("decision deadline expired during final validation")
		}
		if l.OnDecision != nil {
			l.OnDecision(action, time.Since(observedAt))
		}
		var plan ActionPlan
		if l.Plan != nil {
			plan, err = l.Plan(currentObservation, action, active.Deadline)
			if err != nil {
				return fmt.Errorf("plan action: %w", err)
			}
		}
		encoded, err := json.Marshal(action)
		if err != nil {
			return err
		}
		actionID = fmt.Sprintf("%s/%d", active.MatchID, snapshot.Version)
		bound, err := life.Begin(Intent{ID: actionID, Version: snapshot.Version, Action: encoded})
		if err != nil {
			return err
		}
		if l.Journal != nil {
			intent := any(action)
			if l.Plan != nil {
				intent = plan
			}
			if err := l.Journal.Begin(actionID, bound, intent); err != nil {
				return err
			}
		}
		pending, pendingMatch = &action, active.MatchID
		// Execute must revalidate with a fresh observation before delivery.
		decisionTimer.Stop()
		decisionC, pollC = nil, nil
		execCtx, execCancel := context.WithDeadline(ctx, active.Deadline)
		if l.Plan == nil {
			err = l.Execute(execCtx, bound, action, active.Deadline)
		} else {
			pendingPlan := PendingPlan{Plan: plan, MatchID: active.MatchID, After: currentObservation.CapturedAt}
			err = l.drivePlan(execCtx, &pendingPlan, bound, actionID)
		}
		execCancel()
		if err != nil {
			return fmt.Errorf("action unresolved, do not replay: %w", err)
		}
		deliveredAt = time.Now()
		if l.Journal != nil {
			if err = l.Journal.MarkDelivered(actionID); err != nil {
				return err
			}
			deliveredAt, err = l.Journal.DeliveredAt(actionID)
			if err != nil {
				return err
			}
		}
		active = nil
		confirmationDeadline = deliveredAt.Add(l.ConfirmationBudget)
		if l.Plan != nil && plan.Deadline.Before(confirmationDeadline) {
			confirmationDeadline = plan.Deadline
		}
		confirmationTimer = time.NewTimer(time.Until(confirmationDeadline))
		confirmationC = confirmationTimer.C
	}
}
