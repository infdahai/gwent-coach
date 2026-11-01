package matchlog

import (
	"testing"
	"time"
)

func ptr[T any](v T) *T { return &v }
func protocolFixture() []Event {
	at := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC).Format(time.RFC3339Nano)
	first := time.Date(2026, 11, 1, 0, 0, 1, 0, time.UTC)
	second := first.Add(time.Second)
	frames := []FrameEvidence{{Source: "first", MatchID: "m", CapturedAt: first}, {Source: "after", MatchID: "m", CapturedAt: second}}
	return []Event{
		{MatchID: "m", EventType: "action_intent", ActionID: "a", ActionDomain: "gameplay", StateVersion: ptr(uint64(1))},
		{MatchID: "m", EventType: "input_intent", ActionID: "a", Step: ptr(uint64(1)), StateVersion: ptr(uint64(1)), ActionIntent: map[string]string{"kind": "click"}},
		{MatchID: "m", EventType: "input_receipt", ActionID: "a", Step: ptr(uint64(1)), StateVersion: ptr(uint64(1)), EventsSent: ptr(2), DeliveredAt: &at, DeliveryStatus: "delivered"},
		{MatchID: "m", EventType: "input_intent", ActionID: "a", Step: ptr(uint64(2)), StateVersion: ptr(uint64(1)), ActionIntent: map[string]string{"kind": "click"}},
		{MatchID: "m", EventType: "input_receipt", ActionID: "a", Step: ptr(uint64(2)), StateVersion: ptr(uint64(1)), EventsSent: ptr(2), DeliveredAt: &at, DeliveryStatus: "delivered"},
		{MatchID: "m", EventType: "action_delivered", ActionID: "a", StateVersion: ptr(uint64(1)), DeliveredAt: &at},
		{MatchID: "m", EventType: "action_confirmed", ActionID: "a", ActionDomain: "gameplay", StateVersion: ptr(uint64(2)), Confirmation: ptr("confirmed"), FramePath: ptr("after"), CapturedAt: ptr(second.Format(time.RFC3339Nano)), ConfirmationFrames: frames},
	}
}

func TestInputCompletionProtocol(t *testing.T) {
	legacy := protocolFixture()
	marker := func(step uint64) Event {
		return Event{MatchID: "m", EventType: "input_completed", ActionID: "a", Step: ptr(step), StateVersion: ptr(uint64(1))}
	}
	modern := append([]Event{}, legacy[:3]...)
	modern = append(modern, marker(1))
	modern = append(modern, legacy[3:5]...)
	modern = append(modern, marker(2))
	modern = append(modern, legacy[5:]...)
	modern[0].InputProtocol = InputProtocolCompleted
	if _, err := ReplayActions(legacy); err != nil {
		t.Fatalf("legacy delivery: %v", err)
	}
	if samples, err := ConfirmedTrainingEvents(modern); err != nil || len(samples) != 1 {
		t.Fatalf("modern samples=%d err=%v", len(samples), err)
	}
	// A delivered receipt followed by an interrupted hold remains an unresolved tail.
	interrupted := append([]Event{}, modern[:3]...)
	interrupted[1].ActionIntent = map[string]string{"kind": "hold"}
	if traces, err := ReplayActions(interrupted); err != nil || len(traces) != 1 || traces[0].Steps[0].Completed != nil {
		t.Fatalf("interrupted traces=%+v err=%v", traces, err)
	}
	for name, events := range map[string][]Event{
		"missing marker":        append(append([]Event{}, modern[:6]...), modern[7:]...),
		"all markers missing":   func() []Event { e := protocolFixture(); e[0].InputProtocol = InputProtocolCompleted; return e }(),
		"duplicate marker":      append(append(append([]Event{}, modern[:4]...), modern[3]), modern[4:]...),
		"marker before receipt": func() []Event { e := append([]Event{}, modern...); e[2], e[3] = e[3], e[2]; return e }(),
		"wrong step":            func() []Event { e := append([]Event{}, modern...); e[3].Step = ptr(uint64(2)); return e }(),
		"wrong version":         func() []Event { e := append([]Event{}, modern...); e[3].StateVersion = ptr(uint64(2)); return e }(),
		"late marker":           func() []Event { e := append([]Event{}, modern...); e[6], e[7] = e[7], e[6]; return e }(),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ReplayActions(events); err == nil {
				t.Fatal("accepted invalid completion stream")
			}
			if _, err := ConfirmedTrainingEvents(events); err == nil {
				t.Fatal("accepted invalid training stream")
			}
		})
	}
}

func TestTerminalCannotHideUnresolvedInput(t *testing.T) {
	terminal := Event{MatchID: "m", EventType: "match_completed"}
	intent := Event{MatchID: "m", EventType: "action_intent", ActionID: "b", ActionDomain: "gameplay", StateVersion: ptr(uint64(3))}
	standalone := Event{MatchID: "m", EventType: "input_intent", ActionID: "input/1", Step: ptr(uint64(1)), ActionIntent: map[string]string{"kind": "click"}}
	for name, events := range map[string][]Event{
		"pending action":     append(append(protocolFixture(), intent), terminal),
		"pending standalone": append(append(protocolFixture(), standalone), terminal),
		"after terminal":     append(append(protocolFixture(), terminal), Event{MatchID: "m", EventType: "observation"}),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ConfirmedTrainingEvents(events); err == nil {
				t.Fatal("invalid terminal stream accepted")
			}
		})
	}
	if _, err := ReplayActions(append(protocolFixture(), intent)); err != nil {
		t.Fatalf("unsealed tail must remain recoverable: %v", err)
	}
}

func TestTrainingRequiresEveryOrderedInputReceipt(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func([]Event) []Event
	}{
		{"missing-first-receipt", func(e []Event) []Event { return append(e[:2], e[3:]...) }},
		{"missing-final-receipt", func(e []Event) []Event { return append(e[:4], e[5:]...) }},
		{"receipt-without-intent", func(e []Event) []Event { return append(e[:1], e[2:]...) }},
		{"duplicate-receipt", func(e []Event) []Event { return append(e[:3], append([]Event{e[2]}, e[3:]...)...) }},
		{"duplicate-intent", func(e []Event) []Event { e[3].Step = ptr(uint64(1)); return e }},
		{"step-gap", func(e []Event) []Event { e[3].Step = ptr(uint64(3)); return e }},
		{"out-of-order", func(e []Event) []Event { e[2], e[3] = e[3], e[2]; return e }},
		{"partial", func(e []Event) []Event { e[4].DeliveryStatus = "partial"; return e }},
		{"version-changed", func(e []Event) []Event { e[4].StateVersion = ptr(uint64(9)); return e }},
		{"foreign-confirmation", func(e []Event) []Event { e[6].ConfirmationFrames[0].MatchID = "other"; return e }},
		{"predelivery-confirmation", func(e []Event) []Event {
			e[6].ConfirmationFrames[0].CapturedAt = time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC)
			return e
		}},
		{"reused-confirmation-frame", func(e []Event) []Event { e[6].ConfirmationFrames[0].Source = "after"; return e }},
		{"missing-confirmation-evidence", func(e []Event) []Event { e[6].ConfirmationFrames = nil; return e }},
		{"confirmation-before-delivery", func(e []Event) []Event { e[5], e[6] = e[6], e[5]; return e }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			events := tc.mutate(protocolFixture())
			if _, err := ReplayActions(events); err == nil {
				t.Fatal("recovery accepted broken stream")
			}
			if _, err := ConfirmedTrainingEvents(events); err == nil {
				t.Fatal("training accepted broken stream")
			}
		})
	}
	if samples, err := ConfirmedTrainingEvents(protocolFixture()); err != nil || len(samples) != 1 {
		t.Fatalf("valid samples=%d err=%v", len(samples), err)
	}
	if samples, err := ConfirmedTrainingEvents(protocolFixture()[:4]); err != nil || len(samples) != 0 {
		t.Fatalf("unfinished tail samples=%d err=%v", len(samples), err)
	}
}
