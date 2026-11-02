package desktop

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestHoldDurationAndReceipt(t *testing.T) {
	start := time.Now()
	var events []bool
	checks := 0
	r, err := deliverHold(context.Background(), func() error { checks++; return nil }, func(down bool) (uint32, error) {
		events = append(events, down)
		if !down && time.Since(start) < 1500*time.Millisecond {
			t.Error("released early")
		}
		return 1, nil
	})
	if err != nil || r.Outcome != "delivered_unconfirmed" || r.EventsSent != 2 || !reflect.DeepEqual(events, []bool{true, false}) || checks < 2 {
		t.Fatalf("receipt=%+v err=%v events=%v checks=%d", r, err, events, checks)
	}
}

func TestHoldInterruptedDeliveryNeverReplays(t *testing.T) {
	for _, mode := range []string{"before-down", "cancel", "deadline", "guard", "down-failure", "release-failure"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "before-down" {
				cancel()
			}
			if mode == "deadline" {
				var stop context.CancelFunc
				ctx, stop = context.WithTimeout(ctx, 20*time.Millisecond)
				defer stop()
			}
			sentinel := errors.New("guard changed")
			var events []bool
			check := func() error {
				if err := ctx.Err(); err != nil {
					return err
				}
				if mode == "guard" && len(events) > 0 {
					return sentinel
				}
				return nil
			}
			r, err := deliverHold(ctx, check, func(down bool) (uint32, error) {
				events = append(events, down)
				if down && mode == "down-failure" {
					return 0, errors.New("down failed")
				}
				if down && (mode == "cancel" || mode == "release-failure") {
					cancel()
				}
				if !down && mode == "release-failure" {
					return 0, errors.New("release failed")
				}
				return 1, nil
			})
			if err == nil || r.Outcome == "delivered_unconfirmed" {
				t.Fatalf("false success: %+v %v", r, err)
			}
			switch mode {
			case "before-down":
				if len(events) != 0 || r.EventsSent != 0 || r.Outcome != "cursor_moved" {
					t.Fatalf("input after cancel: %+v %v", r, events)
				}
			case "down-failure":
				if !reflect.DeepEqual(events, []bool{true}) || r.EventsSent != 0 {
					t.Fatalf("replayed down: %+v %v", r, events)
				}
			default:
				if !reflect.DeepEqual(events, []bool{true, false}) {
					t.Fatalf("missing release or replay: %v", events)
				}
				want := uint32(2)
				if mode == "release-failure" {
					want = 1
				}
				if r.EventsSent != want || r.Outcome != "partial_or_unknown" {
					t.Fatalf("lost uncertainty: %+v", r)
				}
			}
			if mode == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if mode == "deadline" && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatal(err)
			}
			if mode == "guard" && !errors.Is(err, sentinel) {
				t.Fatal(err)
			}
		})
	}
}
