package observe

import (
	"errors"
	"fmt"
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
			n := Navigation{StateDir: t.TempDir(), InterlockDir: t.TempDir(), StopFile: "pause", Steps: 3, plan: NavigationPlan{Transitions: []Transition{{From: "main", To: "modes"}}}}
			sends := 0
			frames := 0
			capture := func() (navFrame, error) {
				frames++
				at := time.Now()
				if tc.stale {
					at = at.Add(-3 * time.Second)
				}
				return navFrame{Phase: tc.phase, Captured: at, Reference: fmt.Sprint(frames)}, nil
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
	n := Navigation{StateDir: t.TempDir(), InterlockDir: t.TempDir(), StopFile: "pause", Steps: 3, plan: NavigationPlan{Transitions: []Transition{{From: "main", To: "modes"}}}}
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
		return navFrame{Phase: phase, Captured: time.Now(), Reference: fmt.Sprint(captures)}, nil
	}, func(navFrame, Transition) error { sends++; return nil }, func() error { return nil })
	if err == nil || captures != 3 || sends != 1 {
		t.Fatalf("err=%v captures=%d sends=%d", err, captures, sends)
	}
}
func TestNavigationVerifiedRoute(t *testing.T) {
	phase := "training-menu"
	sends := 0
	n := Navigation{StateDir: t.TempDir(), InterlockDir: t.TempDir(), StopFile: "pause", Steps: 4, plan: NavigationPlan{Transitions: []Transition{{From: "training-menu", To: "training"}}}}
	frames := 0
	e := runNavigation(n, func() (navFrame, error) {
		frames++
		return navFrame{Phase: phase, Captured: time.Now(), Reference: fmt.Sprint(frames)}, nil
	}, func(_ navFrame, tr Transition) error { sends++; phase = tr.To; return nil }, func() error { return nil })
	if e != nil || sends != 1 {
		t.Fatalf("%v sends %d", e, sends)
	}
}

func TestNavigationPlanRequiresTargetEvidenceAndRoute(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 1920, 1080))
	target := Anchor{X: 100, Y: 100, Width: 32, Height: 16, MaxError: .05}
	target.Pixels = AnchorPixels(img, target, 1920, 1080)
	other := Anchor{X: 200, Y: 100, Width: 32, Height: 16, MaxError: .05}
	other.Pixels = AnchorPixels(img, other, 1920, 1080)
	m := &PhaseModel{Width: 1920, Height: 1080, Phases: []Phase{{Name: "home", Anchors: []Anchor{target, other}}, {Name: "training", Anchors: []Anchor{other, target}}}}
	plan := NavigationPlan{Transitions: []Transition{{From: "home", To: "training", X: 110, Y: 105, Target: image.Rect(100, 100, 132, 116)}}}
	if err := m.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := plan.validate(m); err != nil {
		t.Fatal(err)
	}
	m.Phases[0].Variants = [][]Anchor{{other, other}}
	if err := plan.validate(m); err == nil {
		t.Fatal("variant without target accepted")
	}
	m.Phases[0].Variants = nil
	plan.Transitions[0].X = 500
	if err := plan.validate(m); err == nil {
		t.Fatal("point outside target accepted")
	}
	plan.Transitions[0].X = 110
	plan.Transitions[0].To = "home"
	if err := plan.validate(m); err == nil {
		t.Fatal("cycle accepted")
	}
}

func TestNavigationRejectsReusedFrames(t *testing.T) {
	n := Navigation{StateDir: t.TempDir(), InterlockDir: t.TempDir(), StopFile: "pause", Steps: 1, plan: NavigationPlan{Transitions: []Transition{{From: "home", To: "training"}}}}
	sends := 0
	err := runNavigation(n, func() (navFrame, error) { return navFrame{Phase: "home", Reference: "same", Captured: time.Now()}, nil }, func(navFrame, Transition) error { sends++; return nil }, func() error { return nil })
	if err == nil || sends != 0 {
		t.Fatalf("err=%v sends=%d", err, sends)
	}
}

func TestNavigationRejectsChangedTargetBeforeInput(t *testing.T) {
	original := image.NewRGBA(image.Rect(0, 0, 1920, 1080))
	changed := image.NewRGBA(original.Bounds())
	for y := 100; y < 116; y++ {
		for x := 100; x < 132; x++ {
			changed.Set(x, y, color.RGBA{255, 255, 255, 255})
		}
	}
	target := Anchor{X: 100, Y: 100, Width: 32, Height: 16, MaxError: .05}
	target.Pixels = AnchorPixels(original, target, 1920, 1080)
	other := Anchor{X: 200, Y: 100, Width: 32, Height: 16, MaxError: .05}
	other.Pixels = AnchorPixels(original, other, 1920, 1080)
	m := PhaseModel{Width: 1920, Height: 1080, Phases: []Phase{{Name: "home", Anchors: []Anchor{target, other}}}}
	n := Navigation{StateDir: t.TempDir(), InterlockDir: t.TempDir(), StopFile: "pause", Steps: 1, plan: NavigationPlan{Transitions: []Transition{{From: "home", To: "training"}}}}
	captures, sends := 0, 0
	err := runNavigation(n, func() (navFrame, error) {
		captures++
		img := image.Image(original)
		if captures > 1 {
			img = changed
		}
		phase, _ := m.Recognize(img)
		return navFrame{Phase: phase, Reference: fmt.Sprint(captures), Captured: time.Now()}, nil
	}, func(navFrame, Transition) error { sends++; return nil }, func() error { return nil })
	if err == nil || sends != 0 {
		t.Fatalf("changed target accepted: err=%v sends=%d", err, sends)
	}
}

func TestNavigationRejectsReusedPostconditionFrame(t *testing.T) {
	n := Navigation{StateDir: t.TempDir(), InterlockDir: t.TempDir(), StopFile: "pause", Steps: 1, plan: NavigationPlan{Transitions: []Transition{{From: "home", To: "training"}}}}
	captures, sends := 0, 0
	err := runNavigation(n, func() (navFrame, error) {
		captures++
		if captures <= 2 {
			return navFrame{Phase: "home", Reference: fmt.Sprint(captures), Captured: time.Now()}, nil
		}
		return navFrame{Phase: "training", Reference: "reused", Captured: time.Now()}, nil
	}, func(navFrame, Transition) error { sends++; return nil }, func() error { return nil })
	if err == nil || sends != 1 {
		t.Fatalf("reused postcondition accepted: err=%v sends=%d", err, sends)
	}
}
