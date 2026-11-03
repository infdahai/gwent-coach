package control

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gwentcoachmvp/internal/desktop"
)

func TestHoldReceiptsAndSingleUse(t *testing.T) {
	for _, partial := range []bool{false, true} {
		t.Run(map[bool]string{false: "delivered", true: "partial"}[partial], func(t *testing.T) {
			b := &fakeBackend{}
			if partial {
				b.click = func(context.Context) (desktop.Receipt, error) {
					return desktop.Receipt{Outcome: "partial_or_unknown", EventsSent: 1}, errors.New("delivery unresolved")
				}
			}
			h, c := setup(t, b)
			ref := capture(t, c)
			cmd := Command{ID: NewID(), Kind: "hold", Reference: ref.Reference, X: 5, Y: 6}
			if _, err := c.Submit(context.Background(), cmd); err != nil {
				t.Fatal(err)
			}
			r := await(t, c, cmd.ID)
			state, outcome, count := "completed", "delivered_unconfirmed", uint32(2)
			if partial {
				state, outcome, count = "failed", "partial_or_unknown", 1
			}
			if r.Command != "hold" || r.State != state || r.Input == nil || r.Input.Outcome != outcome || r.Input.EventsSent != count {
				t.Fatalf("receipt %+v", r)
			}
			assertIntent(t, r, ref.Reference, 5, 6)
			cmd.Kind, cmd.X, cmd.Y = "click", 9, 10
			if prior, err := c.Submit(context.Background(), cmd); err == nil || prior.Command != "hold" {
				t.Fatalf("duplicate %+v %v", prior, err)
			} else {
				assertIntent(t, prior, ref.Reference, 5, 6)
			}
			cmd.ID = NewID()
			if _, err := c.Submit(context.Background(), cmd); err == nil {
				t.Fatal("hold reference reused for click")
			}
			if b.holds.Load() != 1 || b.clicks.Load() != 0 {
				t.Fatal("input replayed or wrong dispatch")
			}
			if _, err := os.Stat(filepath.Join(h.cfg.Root, "captures", ref.Reference+".input-used")); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestHoldAdmissionGuards(t *testing.T) {
	for _, mode := range []string{"expired", "future", "missing-reference", "bounds", "stop"} {
		t.Run(mode, func(t *testing.T) {
			b := &fakeBackend{capture: func(context.Context) (desktop.Capture, error) {
				at := time.Now()
				if mode == "expired" {
					at = at.Add(-time.Minute)
				}
				if mode == "future" {
					at = at.Add(time.Minute)
				}
				return desktop.Capture{At: at, Window: desktop.Window{Client: desktop.Rect{Right: 100, Bottom: 100}}}, nil
			}}
			h, c := setup(t, b)
			ref := capture(t, c)
			cmd := Command{ID: NewID(), Kind: "hold", Reference: ref.Reference, X: 5, Y: 6}
			if mode == "missing-reference" {
				cmd.Reference = ""
			}
			if mode == "bounds" {
				cmd.X = 100
			}
			if mode == "stop" {
				if err := os.WriteFile(h.cfg.StopFile, []byte("stop"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := c.Submit(context.Background(), cmd); err == nil {
				t.Fatal("invalid hold admitted")
			}
			if b.holds.Load() != 0 || b.clicks.Load() != 0 {
				t.Fatal("invalid input reached backend")
			}
		})
	}
}

func TestStopCancelsHoldAndBlocksOtherInput(t *testing.T) {
	started := make(chan struct{})
	b := &fakeBackend{click: func(ctx context.Context) (desktop.Receipt, error) {
		close(started)
		<-ctx.Done()
		return desktop.Receipt{Outcome: "partial_or_unknown", EventsSent: 2}, ctx.Err()
	}}
	_, c := setup(t, b)
	ref := capture(t, c)
	r, err := c.Submit(context.Background(), Command{ID: NewID(), Kind: "hold", Reference: ref.Reference, X: 5, Y: 6})
	if err != nil {
		t.Fatal(err)
	}
	<-started
	for _, kind := range []string{"hold", "click", "capture", "run-session"} {
		if _, err := c.Submit(context.Background(), Command{ID: NewID(), Kind: kind, Reference: ref.Reference, X: 5, Y: 6}); err == nil {
			t.Fatalf("admitted %s while holding", kind)
		}
	}
	submit(t, c, "stop")
	r = await(t, c, r.ID)
	if r.State != "failed" || r.Input == nil || r.Input.Outcome != "partial_or_unknown" || b.holds.Load() != 1 {
		t.Fatalf("cancellation receipt %+v", r)
	}
}
