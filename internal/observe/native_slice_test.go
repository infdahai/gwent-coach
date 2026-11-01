package observe

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"strings"
	"testing"
	"time"
)

func TestNativeSliceSequenceAndUncertainDelivery(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprintf("fail=%v", fail), func(t *testing.T) {
			cfg := NativeSliceConfig{Card: image.Pt(470, 970), Row: image.Pt(960, 780), EndTurn: image.Pt(1845, 540)}
			phases := []string{"slice-ready", "slice-ready", "slice-selected", "slice-placed", "slice-ended"}
			index, sends := 0, 0
			var output bytes.Buffer
			err := runNativeSlice(cfg, func() (navFrame, error) {
				if index >= len(phases) {
					t.Fatal("unexpected capture")
				}
				frame := navFrame{Phase: phases[index], Reference: fmt.Sprint(index), Width: 1920, Height: 1080, Captured: time.Now()}
				index++
				return frame, nil
			}, func(frame navFrame, p image.Point, _ time.Time) error {
				points := []image.Point{cfg.Card, cfg.Row, cfg.EndTurn}
				if p != points[sends] {
					t.Errorf("unexpected point %v", p)
				}
				sends++
				if fail && sends == 2 {
					return errors.New("partial input")
				}
				return nil
			}, func() error { return nil }, func() {}, &output)
			if fail {
				if err == nil || !strings.Contains(err.Error(), "do not replay") || sends != 2 {
					t.Fatalf("err=%v sends=%d", err, sends)
				}
			} else if err != nil || sends != 3 || !strings.Contains(output.String(), `"stage":"end_turn_confirmed"`) {
				t.Fatalf("err=%v sends=%d events=%s", err, sends, output.String())
			}
		})
	}
}

func TestNativeSliceRejectsMissingPointBeforeInput(t *testing.T) {
	sends := 0
	err := runNativeSlice(NativeSliceConfig{}, func() (navFrame, error) { t.Fatal("unexpected capture"); return navFrame{}, nil }, func(navFrame, image.Point, time.Time) error { sends++; return nil }, func() error { return nil }, func() {}, &bytes.Buffer{})
	if err == nil || sends != 0 {
		t.Fatalf("err=%v sends=%d", err, sends)
	}
}
