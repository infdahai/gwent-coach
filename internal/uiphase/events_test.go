package uiphase

import (
	"image"
	"image/color"
	"image/draw"
	"testing"
	"time"
)

func eventFixture() (PhaseModel, map[string]image.Image) {
	images := map[string]image.Image{}
	for name, shade := range map[string]uint8{"menu": 32, "dialog": 224, "unknown": 128} {
		img := image.NewRGBA(image.Rect(0, 0, 32, 16))
		draw.Draw(img, img.Bounds(), image.NewUniform(color.RGBA{shade, shade, shade, 255}), image.Point{}, draw.Src)
		images[name] = img
	}
	model := PhaseModel{Width: 32, Height: 16}
	for _, name := range []string{"menu", "dialog"} {
		anchors := []Anchor{{X: 0, Y: 0, Width: 16, Height: 8, MaxError: .01}, {X: 16, Y: 8, Width: 16, Height: 8, MaxError: .01}}
		for i := range anchors {
			anchors[i].Pixels = AnchorPixels(images[name], anchors[i], 32, 16)
		}
		model.Phases = append(model.Phases, Phase{Name: name, Anchors: anchors})
	}
	return model, images
}

func eventFrame(id string, at time.Time, img image.Image) Frame {
	return Frame{Ref: FrameRef{ID: id, StreamID: "desktop", CapturedAt: at, Width: 32, Height: 16}, Image: img}
}

func TestStageEventsRequireConsecutiveFreshEvidence(t *testing.T) {
	model, images := eventFixture()
	r, err := NewRecognizer(model, "desktop", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t0 := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	raw := []string{"menu", "menu", "menu", "dialog", "dialog", "unknown", "menu", "menu"}
	want := []string{"unknown", "menu", "menu", "unknown", "dialog", "unknown", "unknown", "menu"}
	changed := []bool{false, true, false, true, true, true, false, true}
	previous := "unknown"
	for i, name := range raw {
		at := t0.Add(time.Duration(i) * 10 * time.Millisecond)
		f := eventFrame(string(rune('a'+i)), at, images[name])
		e, err := r.Observe(f, at.Add(time.Millisecond))
		if err != nil || e.Phase != want[i] || e.Previous != previous || e.Evidence.Phase != name || e.Changed != changed[i] || e.Confirmed != (want[i] != "unknown") {
			t.Fatalf("frame %d: %+v %v", i, e, err)
		}
		if e.Confirmed && (e.Sources[0].ID != string(rune('a'+i-1)) || e.Sources[1] != f.Ref) {
			t.Fatalf("unbound sources: %+v", e)
		}
		if e.Evidence.ModelID == "" || e.Evidence.Frame != f.Ref {
			t.Fatal("missing evidence binding")
		}
		previous = e.Phase
	}
}

func TestStageInvalidFramesBreakContinuity(t *testing.T) {
	cases := map[string]func(*Frame, *time.Time){
		"duplicate":      func(f *Frame, _ *time.Time) { f.Ref.ID = "a" },
		"out of order":   func(f *Frame, _ *time.Time) { f.Ref.CapturedAt = f.Ref.CapturedAt.Add(-time.Second) },
		"stale":          func(_ *Frame, now *time.Time) { *now = now.Add(2 * time.Second) },
		"future":         func(f *Frame, _ *time.Time) { f.Ref.CapturedAt = f.Ref.CapturedAt.Add(time.Hour) },
		"other stream":   func(f *Frame, _ *time.Time) { f.Ref.StreamID = "other" },
		"wrong geometry": func(f *Frame, _ *time.Time) { f.Ref.Width = 16 },
		"missing image":  func(f *Frame, _ *time.Time) { f.Image = nil },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			model, images := eventFixture()
			r, _ := NewRecognizer(model, "desktop", time.Second)
			t0 := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
			if _, err := r.Observe(eventFrame("a", t0, images["menu"]), t0); err != nil {
				t.Fatal(err)
			}
			bad := eventFrame("b", t0.Add(time.Millisecond), images["menu"])
			now := bad.Ref.CapturedAt
			change(&bad, &now)
			if e, err := r.Observe(bad, now); err == nil || e.Confirmed || e.Evidence.Frame.ID != "" {
				t.Fatalf("accepted invalid frame: %+v %v", e, err)
			}
			for i, id := range []string{"c", "d"} {
				f := eventFrame(id, t0.Add(time.Duration(i+2)*time.Millisecond), images["menu"])
				e, err := r.Observe(f, f.Ref.CapturedAt)
				if err != nil || e.Confirmed != (i == 1) {
					t.Fatalf("invalid frame preserved continuity: %+v %v", e, err)
				}
			}
		})
	}
}

func TestStageExpiredPredecessorCannotConfirm(t *testing.T) {
	model, images := eventFixture()
	r, _ := NewRecognizer(model, "desktop", time.Second)
	t0 := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	_, _ = r.Observe(eventFrame("a", t0, images["menu"]), t0)
	f := eventFrame("b", t0.Add(2*time.Second), images["menu"])
	e, err := r.Observe(f, f.Ref.CapturedAt)
	if err != nil || e.Confirmed || e.Phase != "unknown" {
		t.Fatalf("expired predecessor accepted: %+v %v", e, err)
	}
}

func TestStageAmbiguityAndModelOwnership(t *testing.T) {
	model, images := eventFixture()
	r, err := NewRecognizer(model, "desktop", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	for i := range model.Phases[0].Anchors[0].Pixels {
		model.Phases[0].Anchors[0].Pixels[i] = 1
	}
	at := time.Now()
	e, err := r.Observe(eventFrame("a", at, images["menu"]), at)
	if err != nil || e.Evidence.Phase != "menu" {
		t.Fatalf("model aliases caller: %+v %v", e, err)
	}
	model, images = eventFixture()
	other := model.Phases[0]
	other.Name = "overlap"
	model.Phases = append(model.Phases, other)
	r, err = NewRecognizer(model, "desktop", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	for i, id := range []string{"a", "b"} {
		f := eventFrame(id, at.Add(time.Duration(i)*time.Millisecond), images["menu"])
		e, err := r.Observe(f, f.Ref.CapturedAt)
		if err != nil || e.Evidence.Phase != "unknown" || e.Phase != "unknown" || e.Confirmed {
			t.Fatalf("ambiguous stage promoted: %+v %v", e, err)
		}
	}
}
