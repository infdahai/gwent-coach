package desktop

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// deliverHold starts after guarded cursor positioning. Cleanup releases a sent
// down event even after cancellation; it never repeats the down event.
func deliverHold(ctx context.Context, check func() error, send func(down bool) (uint32, error)) (receipt Receipt, err error) {
	receipt.Outcome = "cursor_moved"
	if err = check(); err != nil {
		return receipt, err
	}
	n, err := send(true)
	receipt.EventsSent = n
	receipt.Outcome = "partial_or_unknown"
	if n == 1 {
		defer func() {
			released, releaseErr := send(false)
			receipt.EventsSent += released
			if released != 1 && releaseErr == nil {
				releaseErr = fmt.Errorf("mouse release unresolved; do not replay")
			}
			err = errors.Join(err, releaseErr)
			if err == nil {
				receipt.Outcome = "delivered_unconfirmed"
			}
		}()
	}
	if err != nil {
		return receipt, err
	}
	if n != 1 {
		return receipt, fmt.Errorf("mouse down unresolved; do not replay")
	}
	timer := time.NewTimer(1500 * time.Millisecond)
	defer timer.Stop()
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return receipt, ctx.Err()
		case <-timer.C:
			return receipt, check()
		case <-tick.C:
			if err = check(); err != nil {
				return receipt, err
			}
		}
	}
}
