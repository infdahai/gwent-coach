package observe

import (
	"encoding/json"
	"fmt"
	"time"

	"gwentcoachmvp/internal/matchlog"
)

type RecordedInputStep struct {
	Number  uint64
	Input   Action
	Receipt *InputReceipt
}

type PendingAction struct {
	ID               string
	Domain           string
	Snapshot         Snapshot
	Action           json.RawMessage
	Steps            []RecordedInputStep
	DeliveryComplete bool
	AutoEnded        bool
}

func (j *ActionJournal) Pending() *PendingAction {
	if j.pending == "" {
		return nil
	}
	out := &PendingAction{ID: j.pending, Domain: j.domain, Snapshot: copySnapshot(j.original), Action: append(json.RawMessage(nil), j.action...), Steps: append([]RecordedInputStep(nil), j.steps...), DeliveryComplete: j.deliveryComplete, AutoEnded: j.autoEnded}
	for i := range out.Steps {
		if out.Steps[i].Receipt != nil {
			r := *out.Steps[i].Receipt
			out.Steps[i].Receipt = &r
		}
	}
	return out
}

func restoredSemanticState(e matchlog.Event) (json.RawMessage, error) {
	data, err := json.Marshal(e.VisibleState)
	if err != nil {
		return nil, err
	}
	if e.ActionDomain != "gameplay" {
		return data, nil
	}
	var o SemanticObservation
	if err = json.Unmarshal(data, &o); err != nil {
		return nil, err
	}
	if o.MatchID == "" || (e.MatchID != "" && o.MatchID != e.MatchID) {
		return nil, fmt.Errorf("persisted gameplay observation belongs to another match")
	}
	return semanticState(o)
}

// RestoreActionJournal reads original actions and input steps. It never invokes
// policy, changes targets, or replays an unconfirmed transport operation.
func RestoreActionJournal(writer durableEvents, events []matchlog.Event) (*ActionJournal, error) {
	if _, err := matchlog.ReplayActions(events); err != nil {
		return nil, err
	}
	j := NewActionJournal(writer)
	for _, e := range events {
		switch e.EventType {
		case "action_intent":
			if j.pending != "" || e.ActionID == "" || e.StateVersion == nil || *e.StateVersion == 0 || e.Phase == nil || e.FramePath == nil {
				return nil, fmt.Errorf("invalid persisted action intent")
			}
			state, err := restoredSemanticState(e)
			if err != nil {
				return nil, err
			}
			action, err := json.Marshal(e.ActionIntent)
			if err != nil {
				return nil, err
			}
			j.pending = e.ActionID
			if e.MatchID != "" {
				j.matchID = e.MatchID
			}
			j.domain = e.ActionDomain
			j.version = *e.StateVersion
			j.step = 0
			j.complete = true
			j.deliveryComplete = false
			j.autoEnded = false
			j.delivered = time.Time{}
			j.steps = nil
			j.original = Snapshot{Version: j.version, Phase: *e.Phase, Source: *e.FramePath, State: state}
			j.action = action
		case "input_intent":
			if e.ActionID != j.pending {
				j.standalone++
				continue
			}
			if e.Step == nil || *e.Step != j.step+1 {
				return nil, fmt.Errorf("input step sequence changed")
			}
			if len(j.steps) > 0 && j.steps[len(j.steps)-1].Receipt == nil {
				return nil, fmt.Errorf("input step missing receipt")
			}
			data, err := json.Marshal(e.ActionIntent)
			if err != nil {
				return nil, err
			}
			var input Action
			if err = json.Unmarshal(data, &input); err != nil {
				return nil, err
			}
			j.step = *e.Step
			j.steps = append(j.steps, RecordedInputStep{Number: j.step, Input: input})
		case "input_receipt":
			if e.ActionID != j.pending {
				continue
			}
			if e.Step == nil || *e.Step != j.step || len(j.steps) == 0 || e.EventsSent == nil || *e.EventsSent < 0 || j.steps[len(j.steps)-1].Receipt != nil {
				return nil, fmt.Errorf("invalid or duplicate input receipt")
			}
			r := InputReceipt{Outcome: e.DeliveryStatus, EventsSent: uint32(*e.EventsSent)}
			if e.DeliveredAt != nil {
				at, err := time.Parse(time.RFC3339Nano, *e.DeliveredAt)
				if err != nil {
					return nil, err
				}
				r.DeliveredAt = at
				j.delivered = at
			}
			j.steps[len(j.steps)-1].Receipt = &r
			if !(r.Outcome == "delivered" && r.EventsSent == 2) && !(r.Outcome == "cursor_moved" && j.steps[len(j.steps)-1].Input.Kind == "hover" && r.EventsSent == 0) {
				j.complete = false
			}
		case "action_auto_ended":
			if e.ActionID != j.pending || !j.complete || j.delivered.IsZero() || j.autoEnded || e.CapturedAt == nil || e.FramePath == nil {
				return nil, fmt.Errorf("invalid recorded automatic end")
			}
			captured, err := time.Parse(time.RFC3339Nano, *e.CapturedAt)
			if err != nil {
				return nil, err
			}
			if err = matchlog.ValidateConfirmation(j.matchID, *e.FramePath, captured, e.ConfirmationFrames, j.delivered); err != nil {
				return nil, err
			}
			j.autoEnded = true
		case "action_delivered":
			if e.ActionID != j.pending || !j.complete || j.delivered.IsZero() || len(j.steps) == 0 || j.steps[len(j.steps)-1].Receipt == nil {
				return nil, fmt.Errorf("delivery completion lacks input receipts")
			}
			j.deliveryComplete = true
		case "action_confirmed":
			if e.ActionID != j.pending || !j.deliveryComplete || e.StateVersion == nil || *e.StateVersion <= j.version || e.Phase == nil || e.FramePath == nil {
				return nil, fmt.Errorf("confirmation lacks delivered action")
			}
			state, err := restoredSemanticState(e)
			if err != nil {
				return nil, err
			}
			j.original = Snapshot{Version: *e.StateVersion, Phase: *e.Phase, Source: *e.FramePath, State: state}
			j.confirmed[e.ActionID] = true
			j.pending = ""
		}
	}
	return j, nil
}
