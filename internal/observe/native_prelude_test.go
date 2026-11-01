package observe

import (
	"bytes"
	"context"
	"image"
	"strings"
	"testing"
	"time"
)

func TestNativePreludeDiscardsOldPrefetchAndConfirmsFreshFrames(t *testing.T) {
	var events bytes.Buffer
	captures := 0
	capture := func(context.Context) (loopFrame, error) {
		captures++
		phase := "result"
		captured := time.Now()
		if captures == 3 {
			captured = captured.Add(-3 * time.Second)
		}
		if captures > 3 {
			phase = "rewards"
		}
		return loopFrame{navFrame: navFrame{Phase: phase, Reference: time.Now().String(), Captured: captured}}, nil
	}
	sent := 0
	send := func(context.Context, loopFrame, string, image.Point, time.Time) error { sent++; return nil }
	err := runNativePrelude(context.Background(), []PreludeTransition{{From: "result", To: "rewards", Point: image.Pt(960, 1027)}}, false, capture, send, &events)
	if err != nil || sent != 1 || !strings.Contains(events.String(), "predelivery_frame_discarded") || !strings.Contains(events.String(), "prelude_confirmed") {
		t.Fatalf("err=%v sent=%d events=%s", err, sent, events.String())
	}
}
