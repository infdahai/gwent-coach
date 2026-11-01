package observe

import (
	"fmt"
	"image"
	"time"

	"gwentcoachmvp/internal/rules"
)

type ActionPlanner struct {
	AllowPassWithCards bool
	Config             NativeLoopConfig
}

func (p ActionPlanner) Build(o SemanticObservation, state LoopState, action rules.Action, deadline time.Time) (ActionPlan, error) {
	plan := ActionPlan{Action: action, Deadline: deadline}
	if len(p.Candidates(o, []rules.Action{action})) != 1 || deadline.IsZero() || !time.Now().Before(deadline) {
		return plan, fmt.Errorf("unsupported or expired action")
	}
	add := func(kind, targetKind, id, layout string, point image.Point) error {
		if layout == "" || point == (image.Point{}) || !point.In(image.Rect(0, 0, 1920, 1080)) {
			return fmt.Errorf("missing target calibration for %s", id)
		}
		plan.Inputs = append(plan.Inputs, PlannedInput{Kind: kind, TargetKind: targetKind, TargetID: id, LayoutID: layout, Point: point})
		return nil
	}
	cfg := p.Config
	switch action.Kind {
	case "pass":
		if err := add("hold", "control", "pass", "turn", cfg.PassPoint); err != nil {
			return plan, err
		}
	case "order":
		if err := add("click", "board-target", action.Card, "turn", cfg.OrderPoint); err != nil {
			return plan, err
		}
		if err := add("click", "board-target", action.Target, cfg.OrderSelectedPhase, cfg.OrderTargets[action.Target]); err != nil {
			return plan, err
		}
	case "play":
		points := state.CardPoints
		if points == nil {
			points = cfg.CardPoints
		}
		slot := -1
		for i, id := range o.HandOrder {
			if id == action.Card {
				slot = i
				break
			}
		}
		if slot < 0 || len(points) != len(o.HandOrder) || action.Row < 0 || action.Row > 1 {
			return plan, fmt.Errorf("missing card or row calibration")
		}
		plan.AutoEnd = state.AutoEndedPhases[action.Card] != ""
		selected, placed := state.SelectedPhases[action.Card], state.PlacedPhases[action.Card]
		if len(o.HandOrder) == 1 && selected == "" && placed == "" {
			selected, placed = cfg.SelectedPhase, cfg.PlacedPhase
		}
		if err := add("click", "hand-instance", action.Card, "turn", points[slot]); err != nil {
			return plan, err
		}
		if err := add("click", "row", fmt.Sprint(action.Row), selected, cfg.RowPoints[action.Row]); err != nil {
			return plan, err
		}
		if action.Target != "" {
			if err := add("click", "board-target", action.Target, state.TargetPhases[action.Card], cfg.TargetPoints[action.Target]); err != nil {
				return plan, err
			}
		}
		if plan.AutoEnd && placed == "" {
			plan.Inputs = append(plan.Inputs, PlannedInput{TargetKind: "control", TargetID: "end-turn"})
		} else if err := add("click", "control", "end-turn", placed, cfg.EndTurnPoint); err != nil {
			return plan, err
		}
	default:
		return plan, fmt.Errorf("unsupported action")
	}
	return plan, nil
}

// PlannedInput names the semantic target as well as its calibrated location.
type PlannedInput struct {
	Kind, TargetKind, TargetID, LayoutID string
	Point                                image.Point
}

// ActionPlan is fixed before the first input and is suitable for persistence.
type ActionPlan struct {
	Action   rules.Action
	Deadline time.Time
	Inputs   []PlannedInput
	AutoEnd  bool
}

// TargetEvidence represents an independently agreed pair of visual captures.
type TargetEvidence struct {
	Kind, ID, LayoutID string
	Bounds             image.Rectangle
	MatchID            string
	Sources            [2]string
	Captured           [2]time.Time
}

// PendingPlan is owned by the controller. Receipt advances exactly one step;
// an uncertain receipt leaves the action unresolved rather than reissuing input.
type PendingPlan struct {
	Plan      ActionPlan
	MatchID   string
	Next      int
	After     time.Time
	Uncertain bool
}

func (p *PendingPlan) NextInput(now time.Time, evidence TargetEvidence) (PlannedInput, error) {
	if p.Uncertain || p.Next < 0 || p.Next >= len(p.Plan.Inputs) {
		return PlannedInput{}, fmt.Errorf("plan has no safely executable input")
	}
	if p.Plan.Deadline.IsZero() || !now.Before(p.Plan.Deadline) {
		return PlannedInput{}, fmt.Errorf("action deadline expired")
	}
	step := p.Plan.Inputs[p.Next]
	if evidence.MatchID != p.MatchID || evidence.Kind != step.TargetKind || evidence.ID != step.TargetID || evidence.LayoutID != step.LayoutID || !step.Point.In(evidence.Bounds) || evidence.Sources[0] == "" || evidence.Sources[1] == "" || evidence.Sources[0] == evidence.Sources[1] || !evidence.Captured[0].After(p.After) || !evidence.Captured[1].After(evidence.Captured[0]) || evidence.Captured[1].After(now) || now.Sub(evidence.Captured[0]) > 2*time.Second {
		return PlannedInput{}, fmt.Errorf("fresh paired target evidence required")
	}
	return step, nil
}

func (p *PendingPlan) RecordReceipt(receipt InputReceipt) error {
	if p.Uncertain || p.Next < 0 || p.Next >= len(p.Plan.Inputs) {
		return fmt.Errorf("no pending input")
	}
	if receipt.Outcome != "delivered" || receipt.EventsSent != 2 || receipt.DeliveredAt.IsZero() || !receipt.DeliveredAt.After(p.After) || receipt.DeliveredAt.After(p.Plan.Deadline) {
		p.Uncertain = true
		return fmt.Errorf("input delivery unresolved; do not replay")
	}
	p.After = receipt.DeliveredAt
	p.Next++
	return nil
}
