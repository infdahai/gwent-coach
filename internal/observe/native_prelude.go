package observe

import (
	"context"
	"encoding/json"
	"fmt"
	"image"
	"io"
	"time"
)

// PreludeTransition binds an observed training navigation phase to its expected
// successor. An uncertain delivery is never retried.
type PreludeTransition struct {
	From, To string
	Also     []string
	Point    image.Point
}

func runNativePrelude(ctx context.Context, transitions []PreludeTransition, verifyOldPrefetch bool, capture func(context.Context) (loopFrame, error), send func(context.Context, loopFrame, string, image.Point, time.Time) error, events io.Writer) error {
	for i := 0; i < len(transitions); {
		t := transitions[i]
		if t.From == "" || t.To == "" || t.From == t.To || !t.Point.In(image.Rect(1, 1, 1920, 1080)) {
			return fmt.Errorf("invalid prelude transition")
		}
		first, err := capture(ctx)
		if err != nil {
			return err
		}
		second, err := capture(ctx)
		if err != nil {
			return err
		}
		if first.Phase != t.From || second.Phase != t.From || first.Reference == second.Reference || time.Since(second.Captured) > 2*time.Second {
			return fmt.Errorf("prelude expected stable %s", t.From)
		}
		deadline := time.Now().Add(10 * time.Second)
		done := make(chan error, 1)
		go func() { done <- send(ctx, second, "click", t.Point, deadline) }()
		prefetched, prefetchErr := capture(ctx)
		err = <-done
		delivered := time.Now()
		if err != nil {
			return fmt.Errorf("prelude input unresolved, do not replay: %w", err)
		}
		if prefetchErr != nil {
			return fmt.Errorf("prelude observation unresolved, do not replay: %w", prefetchErr)
		}
		if verifyOldPrefetch && i == 0 && beforeDelivery(prefetched.Captured, delivered) {
			if wait := time.Until(prefetched.Captured.Add(2100 * time.Millisecond)); wait > 0 {
				select {
				case <-time.After(wait):
				case <-ctx.Done():
					return ctx.Err()
				}
			}
		}
		if beforeDelivery(prefetched.Captured, delivered) {
			if err := json.NewEncoder(events).Encode(map[string]any{"status": "predelivery_frame_discarded", "phase": prefetched.Phase, "age_ms": float64(time.Since(prefetched.Captured)) / float64(time.Millisecond), "captured_at": prefetched.Captured, "delivered_at": delivered}); err != nil {
				return err
			}
		} else {
			return fmt.Errorf("prelude capture ordering unexpected; inspect before resuming")
		}
		consecutive := 0
		confirmed := ""
		until := time.Now().Add(60 * time.Second)
		for time.Now().Before(until) {
			frame, err := capture(ctx)
			if err != nil {
				return err
			}
			accepted := frame.Phase == t.To
			for _, alternative := range t.Also {
				accepted = accepted || frame.Phase == alternative
			}
			if accepted && frame.Captured.After(delivered) {
				if confirmed != frame.Phase {
					consecutive = 0
				}
				confirmed = frame.Phase
				consecutive++
			} else {
				consecutive = 0
				confirmed = ""
			}
			if consecutive == 2 {
				if err := json.NewEncoder(events).Encode(map[string]any{"status": "prelude_confirmed", "from": t.From, "to": confirmed, "reference": frame.Reference}); err != nil {
					return err
				}
				break
			}
			select {
			case <-time.After(300 * time.Millisecond):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		if consecutive < 2 {
			return fmt.Errorf("prelude %s outcome unresolved, do not replay", t.From)
		}
		next := len(transitions)
		for j := i + 1; j < len(transitions); j++ {
			if transitions[j].From == confirmed {
				next = j
				break
			}
		}
		if next == len(transitions) && i != len(transitions)-1 {
			return fmt.Errorf("no prelude transition from confirmed %s", confirmed)
		}
		i = next
	}
	return nil
}
