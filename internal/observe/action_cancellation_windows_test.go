//go:build windows

package observe

import (
	"context"
	"errors"
	"io"
	"log"
	"testing"
)

func TestCancelledActionNeverReachesDesktop(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var receipt InputReceipt
	err := ExecuteContext(ctx, t.TempDir(), log.New(io.Discard, "", 0), Action{
		Kind: "click", Reference: "must-not-open", StopFile: "must-not-open",
		onReceipt: func(r InputReceipt) error { receipt = r; return nil },
	})
	if !errors.Is(err, context.Canceled) || receipt.Outcome != "not_sent" || receipt.EventsSent != 0 {
		t.Fatalf("receipt=%+v err=%v", receipt, err)
	}
	// actionWindow is used again after cursor motion and during a held button.
	if _, err := actionWindow(Action{ctx: ctx}, report{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("guard ignored cancellation: %v", err)
	}
}
