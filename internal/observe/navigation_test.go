package observe

import (
	"errors"
	"image"
	"image/color"
	"testing"
	"time"
)

func TestPhaseRejectsUnknownAndAmbiguity(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 96, 54))
	for y := 0; y < 54; y++ {
		for x := 0; x < 96; x++ {
			img.Set(x, y, color.RGBA{200, 100, 50, 255})
		}
	}
	a := Anchor{X: 2, Y: 2, Width: 32, Height: 16, MaxError: .02}
	a.Pixels = AnchorPixels(img, a, 96, 54)
	m := PhaseModel{Width: 96, Height: 54, Phases: []Phase{{Name: "main", Anchors: []Anchor{a, a}}}}
	if e := m.Validate(); e != nil {
		t.Fatal(e)
	}
	if p, _ := m.Recognize(img); p != "main" {
		t.Fatal(p)
	}
	if p, _ := m.Recognize(image.NewRGBA(img.Bounds())); p != "unknown" {
		t.Fatal(p)
	}
	m.Phases = append(m.Phases, Phase{Name: "modes", Anchors: []Anchor{a, a}})
	if p, _ := m.Recognize(img); p != "unknown" {
		t.Fatal(p)
	}
	if p, _ := m.Recognize(image.NewRGBA(image.Rect(0, 0, 20, 20))); p != "unknown" {
		t.Fatal(p)
	}
}
func TestNavigationFailurePaths(t *testing.T) {
	for _, tc := range []struct {
		name                     string
		phase                    string
		stale, paused, sendError bool
		sends                    int
	}{
		{name: "unknown", phase: "unknown"}, {name: "unsupported", phase: "deck"}, {name: "stale", phase: "main", stale: true}, {name: "paused", phase: "main", paused: true}, {name: "send-error", phase: "main", sendError: true, sends: 1}, {name: "postcondition-timeout", phase: "main", sends: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n := Navigation{StateDir: t.TempDir(), InterlockDir: t.TempDir(), StopFile: "pause", Steps: 3}
			sends := 0
			capture := func() (navFrame, error) {
				at := time.Now()
				if tc.stale {
					at = at.Add(-3 * time.Second)
				}
				return navFrame{Phase: tc.phase, Captured: at}, nil
			}
			send := func(navFrame, Transition) error {
				sends++
				if tc.sendError {
					return errors.New("partial delivery")
				}
				return nil
			}
			pause := func() error {
				if tc.paused {
					return errors.New("paused")
				}
				return nil
			}
			if e := runNavigation(n, capture, send, pause); e == nil {
				t.Fatal("accepted failure")
			}
			if sends != tc.sends {
				t.Fatal(sends)
			}
			if e := runNavigation(n, capture, send, pause); e == nil {
				t.Fatal("reopened consumed session")
			}
			if sends != tc.sends {
				t.Fatal("replayed")
			}
		})
	}
}
func TestUnknownAfterDeliveryStops(t *testing.T) {
	n := Navigation{StateDir: t.TempDir(), InterlockDir: t.TempDir(), StopFile: "pause", Steps: 3}
	captures := 0
	sends := 0
	err := runNavigation(n, func() (navFrame, error) {
		captures++
		phase := "main"
		if captures == 3 {
			phase = "unknown"
		}
		if captures > 3 {
			phase = "modes"
		}
		return navFrame{Phase: phase, Captured: time.Now()}, nil
	}, func(navFrame, Transition) error { sends++; return nil }, func() error { return nil })
	if err == nil || captures != 3 || sends != 1 {
		t.Fatalf("err=%v captures=%d sends=%d", err, captures, sends)
	}
}
func TestNavigationVerifiedRoute(t *testing.T) {
	phase := "training-menu"
	sends := 0
	n := Navigation{StateDir: t.TempDir(), InterlockDir: t.TempDir(), StopFile: "pause", Steps: 4}
	e := runNavigation(n, func() (navFrame, error) { return navFrame{Phase: phase, Captured: time.Now()}, nil }, func(_ navFrame, tr Transition) error { sends++; phase = tr.To; return nil }, func() error { return nil })
	if e != nil || sends != 1 {
		t.Fatalf("%v sends %d", e, sends)
	}
}
