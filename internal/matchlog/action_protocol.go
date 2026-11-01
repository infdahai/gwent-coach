package matchlog

import (
	"encoding/json"
	"fmt"
	"time"
)

const InputProtocolCompleted = 1

type InputStep struct {
	Intent    Event
	Receipt   *Event
	Completed *Event
}
type ActionTrace struct {
	Intent               Event
	Steps                []InputStep
	Delivered, Confirmed *Event
}

func inputKind(e Event) (string, error) {
	data, err := json.Marshal(e.ActionIntent)
	if err != nil {
		return "", err
	}
	var input struct{ Kind string }
	if err = json.Unmarshal(data, &input); err != nil {
		return "", err
	}
	switch input.Kind {
	case "click", "hold", "hover", "escape":
		return input.Kind, nil
	}
	return "", fmt.Errorf("unsupported recorded input kind")
}

func fullStep(step InputStep) bool {
	if step.Receipt == nil || step.Receipt.EventsSent == nil || step.Receipt.DeliveredAt == nil {
		return false
	}
	kind, err := inputKind(step.Intent)
	if err != nil {
		return false
	}
	if kind == "hover" {
		return step.Receipt.DeliveryStatus == "cursor_moved" && *step.Receipt.EventsSent == 0
	}
	return step.Receipt.DeliveryStatus == "delivered" && *step.Receipt.EventsSent == 2
}

// ReplayActions is the shared recovery/training protocol validator. Incomplete
// tails remain recoverable evidence; a completion marker cannot conceal them.
func ReplayActions(events []Event) ([]ActionTrace, error) {
	var traces []ActionTrace
	index := map[string]int{}
	active := map[string]string{}
	standalone := map[string]InputStep{}
	terminal := map[string]bool{}
	fail := func(e Event, why string) ([]ActionTrace, error) {
		return nil, fmt.Errorf("%s action %q step %v: %s", e.EventType, e.ActionID, e.Step, why)
	}
	for _, event := range events {
		e := event
		key := e.MatchID + "\x00" + e.ActionID
		i, known := index[key]
		if terminal[e.MatchID] {
			return fail(e, "event follows terminal record")
		}
		switch e.EventType {
		case "match_completed", "match_incomplete":
			if e.EventType == "match_completed" {
				if active[e.MatchID] != "" {
					return fail(e, "completion hides unresolved action")
				}
				for _, step := range standalone {
					if step.Intent.MatchID == e.MatchID && !fullStep(step) {
						return fail(e, "completion hides unresolved standalone input")
					}
				}
			}
			terminal[e.MatchID] = true
		case "action_intent":
			if e.ActionID == "" || known || active[e.MatchID] != "" || e.StateVersion == nil || *e.StateVersion == 0 {
				return fail(e, "missing, duplicate or overlapping action intent")
			}
			if e.ActionDomain != "gameplay" && e.ActionDomain != "navigation" {
				return fail(e, "unknown action domain")
			}
			if e.InputProtocol != 0 && e.InputProtocol != InputProtocolCompleted {
				return fail(e, "unknown input protocol")
			}
			index[key] = len(traces)
			traces = append(traces, ActionTrace{Intent: e})
			active[e.MatchID] = key
		case "input_intent":
			if e.ActionID == "" || e.Step == nil || *e.Step == 0 {
				return fail(e, "missing input identity")
			}
			if _, err := inputKind(e); err != nil {
				return fail(e, err.Error())
			}
			if !known {
				if e.StateVersion != nil || active[e.MatchID] != "" {
					return fail(e, "unbound semantic input")
				}
				if _, exists := standalone[key]; exists {
					return fail(e, "duplicate standalone input")
				}
				standalone[key] = InputStep{Intent: e}
				continue
			}
			t := &traces[i]
			if active[e.MatchID] != key || t.Delivered != nil || t.Confirmed != nil || e.StateVersion == nil || *e.StateVersion != *t.Intent.StateVersion || *e.Step != uint64(len(t.Steps)+1) {
				return fail(e, "input order or semantic version changed")
			}
			if len(t.Steps) > 0 {
				previous := t.Steps[len(t.Steps)-1]
				if !fullStep(previous) {
					return fail(e, "previous step lacks full receipt")
				}
				if t.Intent.InputProtocol == InputProtocolCompleted && previous.Completed == nil {
					return fail(e, "previous step lacks execution completion")
				}
			}
			t.Steps = append(t.Steps, InputStep{Intent: e})
		case "input_receipt":
			if e.Step == nil || e.EventsSent == nil || *e.EventsSent < 0 {
				return fail(e, "missing receipt identity/count")
			}
			var step *InputStep
			if known {
				t := &traces[i]
				if active[e.MatchID] != key || t.Delivered != nil || t.Confirmed != nil || len(t.Steps) == 0 || e.StateVersion == nil || *e.StateVersion != *t.Intent.StateVersion {
					return fail(e, "receipt outside active input")
				}
				step = &t.Steps[len(t.Steps)-1]
			} else {
				v, exists := standalone[key]
				if !exists || e.StateVersion != nil {
					return fail(e, "receipt has no input intent")
				}
				step = &v
			}
			if step.Receipt != nil || step.Intent.Step == nil || *e.Step != *step.Intent.Step {
				return fail(e, "duplicate or out-of-order receipt")
			}
			if e.DeliveredAt != nil {
				if _, err := time.Parse(time.RFC3339Nano, *e.DeliveredAt); err != nil {
					return fail(e, "invalid delivery timestamp")
				}
			}
			step.Receipt = &e
			if !known {
				standalone[key] = *step
			}
		case "input_completed":
			if !known {
				return fail(e, "completion has no original action")
			}
			t := &traces[i]
			if active[e.MatchID] != key || t.Delivered != nil || t.Confirmed != nil || len(t.Steps) == 0 || e.StateVersion == nil || *e.StateVersion != *t.Intent.StateVersion || e.Step == nil {
				return fail(e, "completion outside active input")
			}
			step := &t.Steps[len(t.Steps)-1]
			if *e.Step != *step.Intent.Step || step.Completed != nil || !fullStep(*step) {
				return fail(e, "invalid or duplicate input completion")
			}
			for _, earlier := range t.Steps[:len(t.Steps)-1] {
				if earlier.Completed == nil {
					return fail(e, "earlier step lacks execution completion")
				}
			}
			step.Completed = &e
		case "action_auto_ended":
			if !known || active[e.MatchID] != key || traces[i].Delivered != nil || len(traces[i].Steps) == 0 || e.StateVersion == nil || *e.StateVersion != *traces[i].Intent.StateVersion || e.FramePath == nil || e.CapturedAt == nil {
				return fail(e, "automatic end lacks active delivered input")
			}
			last := traces[i].Steps[len(traces[i].Steps)-1]
			if !fullStep(last) {
				return fail(e, "automatic end lacks full receipt")
			}
			if (traces[i].Intent.InputProtocol == InputProtocolCompleted || traces[i].Steps[0].Completed != nil) && last.Completed == nil {
				return fail(e, "automatic end lacks execution completion")
			}
			captured, err := time.Parse(time.RFC3339Nano, *e.CapturedAt)
			if err != nil {
				return fail(e, "invalid automatic end timestamp")
			}
			delivered, err := time.Parse(time.RFC3339Nano, *last.Receipt.DeliveredAt)
			if err != nil {
				return fail(e, "invalid input timestamp")
			}
			if err = ValidateConfirmation(e.MatchID, *e.FramePath, captured, e.ConfirmationFrames, delivered); err != nil {
				return fail(e, err.Error())
			}
		case "action_delivered":
			if !known {
				return fail(e, "delivery has no original action")
			}
			t := &traces[i]
			if active[e.MatchID] != key || t.Delivered != nil || t.Confirmed != nil || len(t.Steps) == 0 || e.StateVersion == nil || *e.StateVersion != *t.Intent.StateVersion {
				return fail(e, "invalid or duplicate delivery completion")
			}
			for _, step := range t.Steps {
				if !fullStep(step) {
					return fail(e, "completion hides missing or partial receipt")
				}
				if (t.Intent.InputProtocol == InputProtocolCompleted || t.Steps[0].Completed != nil) && step.Completed == nil {
					return fail(e, "completion hides unfinished input execution")
				}
			}
			last := t.Steps[len(t.Steps)-1].Receipt
			if e.DeliveredAt == nil || *e.DeliveredAt != *last.DeliveredAt {
				return fail(e, "completion timestamp differs from final receipt")
			}
			t.Delivered = &e
		case "action_confirmed":
			if !known {
				return fail(e, "confirmation has no original action")
			}
			t := &traces[i]
			if active[e.MatchID] != key || t.Delivered == nil || t.Confirmed != nil || e.ActionDomain != t.Intent.ActionDomain || e.StateVersion == nil || *e.StateVersion <= *t.Intent.StateVersion || e.Confirmation == nil || *e.Confirmation != "confirmed" || e.FramePath == nil || *e.FramePath == "" {
				return fail(e, "confirmation lacks complete ordered delivery")
			}
			if e.CapturedAt == nil {
				return fail(e, "confirmation capture time missing")
			}
			captured, err := time.Parse(time.RFC3339Nano, *e.CapturedAt)
			if err != nil {
				return fail(e, "invalid confirmation capture time")
			}
			delivered, err := time.Parse(time.RFC3339Nano, *t.Delivered.DeliveredAt)
			if err != nil {
				return fail(e, "invalid delivery time")
			}
			if err = ValidateConfirmation(e.MatchID, *e.FramePath, captured, e.ConfirmationFrames, delivered); err != nil {
				return fail(e, err.Error())
			}
			t.Confirmed = &e
			delete(active, e.MatchID)
		}
	}
	return traces, nil
}
