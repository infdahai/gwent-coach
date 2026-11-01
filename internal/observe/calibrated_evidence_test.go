package observe

import (
	"image"
	"image/color"
	"image/draw"
	"testing"
	"time"

	"gwentcoachmvp/internal/rules"
	"gwentcoachmvp/internal/stateestimate"
	"gwentcoachmvp/internal/uiphase"
)

func evidenceModel(images []image.Image, labels []string, x int) PhaseModel {
	m := PhaseModel{Width: 192, Height: 64}
	for i, img := range images {
		anchors := []Anchor{{X: x, Y: 0, Width: 16, Height: 8, MaxError: .01}, {X: x + 16, Y: 0, Width: 16, Height: 8, MaxError: .01}}
		for j := range anchors {
			anchors[j].Pixels = AnchorPixels(img, anchors[j], 192, 64)
		}
		m.Phases = append(m.Phases, Phase{Name: labels[i], Anchors: anchors})
	}
	return m
}

func evidenceFact[T any](images []image.Image, labels []string, values []T, x int) *FactCalibration[T] {
	cal := &FactCalibration[T]{Model: evidenceModel(images, labels, x), Values: map[string]T{}, RequiredRGB: []RGBRequirement{{ROI: image.Rect(x, 0, x+16, 8), MaxError: .01}}}
	for i, label := range labels {
		cal.Values[label] = values[i]
	}
	return cal
}

func evidenceFixture() (EvidenceCalibration, [2]*image.RGBA) {
	var frames [2]*image.RGBA
	for i := range frames {
		frames[i] = image.NewRGBA(image.Rect(0, 0, 192, 64))
		for group := 0; group < 6; group++ {
			c := color.RGBA{R: 50, G: 80, B: 100, A: 255}
			if i == 1 && (group == 3 || group == 4) {
				c = color.RGBA{R: 180, G: 200, B: 220, A: 255}
			}
			draw.Draw(frames[i], image.Rect(group*32, 0, group*32+32, 8), image.NewUniform(c), image.Point{}, draw.Src)
		}
	}
	one := []image.Image{frames[0]}
	two := []image.Image{frames[0], frames[1]}
	cards := stateestimate.CardsFacts{Visible: []rules.Instance{{ID: "own", CardID: 152405, Power: 3, Zone: rules.Hand, Row: -1}}, HandOrder: []string{"own"}}
	other := stateestimate.CardsFacts{Visible: append(append([]rules.Instance{}, cards.Visible...), rules.Instance{ID: "enemy", CardID: 152405, Owner: 1, Power: 5, Zone: rules.Board, Row: 0}), HandOrder: []string{"own"}}
	return EvidenceCalibration{
		Stage:   evidenceModel(one, []string{"turn"}, 0),
		Round:   evidenceFact(one, []string{"round-one"}, []stateestimate.RoundFacts{{Round: 1}}, 32),
		Turn:    evidenceFact(one, []string{"local-active"}, []stateestimate.TurnFacts{{Player: 0}}, 64),
		Totals:  evidenceFact(two, []string{"empty-board", "enemy-five"}, []stateestimate.TotalsFacts{{HandCount: [2]int{1, 1}}, {Score: [2]int{0, 5}, HandCount: [2]int{1, 1}}}, 96),
		Cards:   evidenceFact(two, []string{"empty-board", "enemy-five"}, []stateestimate.CardsFacts{cards, other}, 128),
		Outcome: evidenceFact(one, []string{"ongoing"}, []stateestimate.OutcomeFacts{{Winner: -1}}, 160),
	}, frames
}

func readEvidencePair(t *testing.T, r *CalibratedEvidenceReader, img image.Image) ([2]stateestimate.Evidence, time.Time) {
	t.Helper()
	now := time.Date(2026, 11, 1, 0, 0, 1, 0, time.UTC)
	var pair [2]stateestimate.Evidence
	for i, id := range []string{"a", "b"} {
		var err error
		pair[i], err = r.Read(stateestimate.FrameRef{ID: id, MatchID: "m", Player: 0, Width: 192, Height: 64, CapturedAt: now.Add(time.Duration(i-2) * 100 * time.Millisecond)}, img)
		if err != nil {
			t.Fatal(err)
		}
	}
	return pair, now
}

func TestCalibratedEvidenceSeparatesStageAndBoard(t *testing.T) {
	cal, frames := evidenceFixture()
	reader, err := NewCalibratedEvidenceReader(cal)
	if err != nil {
		t.Fatal(err)
	}
	var observed [2]stateestimate.Observation
	for i, img := range frames {
		pair, now := readEvidencePair(t, reader, img)
		observed[i], err = stateestimate.Estimate(pair, now, time.Second)
		if err != nil || observed[i].View == nil || !observed[i].Eligible {
			t.Fatalf("scene %d: %+v %v", i, observed[i], err)
		}
	}
	if observed[0].Stage != observed[1].Stage || observed[0].StateKey == observed[1].StateKey || observed[0].View.Score[1] != 0 || observed[1].View.Score[1] != 5 {
		t.Fatal("stage collapsed different boards")
	}
	// Change only the card evidence. The UI still says turn; the game state must not.
	draw.Draw(frames[0], image.Rect(128, 0, 160, 8), image.Black, image.Point{}, draw.Src)
	pair, now := readEvidencePair(t, reader, frames[0])
	unknown, err := stateestimate.Estimate(pair, now, time.Second)
	if err != nil || unknown.Stage != "turn" || unknown.Facts.Cards != nil || unknown.View != nil || unknown.Eligible {
		t.Fatalf("unknown card evidence promoted: %+v %v", unknown, err)
	}
}

func TestCalibratedEvidenceRejectsMissingVariantIdentity(t *testing.T) {
	cal, _ := evidenceFixture()
	cal.Cards.Model.Phases[0].Variants = [][]Anchor{cal.Stage.Phases[0].Anchors}
	if _, err := NewCalibratedEvidenceReader(cal); err == nil {
		t.Fatal("accepted fact variant without card evidence")
	}
}

func TestCalibratedEvidenceOwnsConfigurationAndOutputs(t *testing.T) {
	cal, frames := evidenceFixture()
	reader, err := NewCalibratedEvidenceReader(cal)
	if err != nil {
		t.Fatal(err)
	}
	cal.Stage.Phases[0].Anchors[0].Pixels[0] = 1
	v := cal.Cards.Values["empty-board"]
	v.Visible[0].CardID = 999
	pair, _ := readEvidencePair(t, reader, frames[0])
	if pair[0].Stage != "turn" || pair[0].Facts.Cards.Visible[0].CardID != 152405 {
		t.Fatal("configuration aliases reader")
	}
	pair[0].Facts.Cards.Visible[0].CardID = 999
	again, _ := readEvidencePair(t, reader, frames[0])
	if again[0].Facts.Cards.Visible[0].CardID != 152405 {
		t.Fatal("output aliases reader")
	}
}

func TestCalibratedEvidenceAmbiguousFactStaysUnknown(t *testing.T) {
	cal, frames := evidenceFixture()
	alias := cal.Cards.Model.Phases[0]
	alias.Name = "ambiguous"
	cal.Cards.Model.Phases = append(cal.Cards.Model.Phases, alias)
	cal.Cards.Values[alias.Name] = cal.Cards.Values["empty-board"]
	reader, err := NewCalibratedEvidenceReader(cal)
	if err != nil {
		t.Fatal(err)
	}
	pair, now := readEvidencePair(t, reader, frames[0])
	o, err := stateestimate.Estimate(pair, now, time.Second)
	if err != nil || o.Stage != "turn" || o.Facts.Cards != nil || o.View != nil || o.Eligible {
		t.Fatalf("ambiguous fact promoted: %+v %v", o, err)
	}
}

func TestCalibratedEvidenceMissingFactModel(t *testing.T) {
	cal, frames := evidenceFixture()
	cal.Totals = nil
	reader, err := NewCalibratedEvidenceReader(cal)
	if err != nil {
		t.Fatal(err)
	}
	pair, now := readEvidencePair(t, reader, frames[0])
	o, err := stateestimate.Estimate(pair, now, time.Second)
	if err != nil || o.Facts.Totals != nil || o.View != nil || o.Eligible {
		t.Fatalf("missing model supplied totals: %+v %v", o, err)
	}
}

func TestCalibratedEvidenceConsumesBoundStageEvidence(t *testing.T) {
	cal, frames := evidenceFixture()
	reader, err := NewCalibratedEvidenceReader(cal)
	if err != nil {
		t.Fatal(err)
	}
	stages, err := uiphase.NewRecognizer(cal.Stage, "m", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 1, 0, 0, 1, 0, time.UTC)
	var pair [2]stateestimate.Evidence
	var event uiphase.Event
	var ref stateestimate.FrameRef
	for i, id := range []string{"a", "b"} {
		ref = stateestimate.FrameRef{ID: id, MatchID: "m", CapturedAt: now.Add(time.Duration(i-2) * 100 * time.Millisecond), Width: 192, Height: 64, Player: 0}
		event, err = stages.Observe(uiphase.Frame{Ref: uiphase.FrameRef{ID: id, StreamID: "m", CapturedAt: ref.CapturedAt, Width: 192, Height: 64}, Image: frames[0]}, now)
		if err != nil {
			t.Fatal(err)
		}
		if event.Confirmed != (i == 1) {
			t.Fatal("unexpected UI confirmation")
		}
		pair[i], err = reader.ReadWithStage(ref, frames[0], event.Evidence)
		if err != nil {
			t.Fatal(err)
		}
	}
	o, err := stateestimate.Estimate(pair, now, time.Second)
	if err != nil || !o.Eligible || o.View == nil || o.Stage != "turn" {
		t.Fatalf("stage/facts connection: %+v %v", o, err)
	}
	for name, change := range map[string]func(*uiphase.Evidence){
		"frame":     func(e *uiphase.Evidence) { e.Frame.ID = "other" },
		"stream":    func(e *uiphase.Evidence) { e.Frame.StreamID = "other" },
		"model":     func(e *uiphase.Evidence) { e.ModelID = "other" },
		"timestamp": func(e *uiphase.Evidence) { e.Frame.CapturedAt = e.Frame.CapturedAt.Add(time.Millisecond) },
		"label":     func(e *uiphase.Evidence) { e.Phase = "not-configured" },
	} {
		t.Run(name, func(t *testing.T) {
			bad := event.Evidence
			change(&bad)
			if _, err := reader.ReadWithStage(ref, frames[0], bad); err == nil {
				t.Fatal("accepted mismatched stage evidence")
			}
		})
	}
}

func BenchmarkCalibratedEvidenceReader(b *testing.B) {
	cal, frames := evidenceFixture()
	reader, err := NewCalibratedEvidenceReader(cal)
	if err != nil {
		b.Fatal(err)
	}
	ref := stateestimate.FrameRef{ID: "frame", MatchID: "m", Player: 0, Width: 192, Height: 64, CapturedAt: time.Now()}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := reader.Read(ref, frames[0]); err != nil {
			b.Fatal(err)
		}
	}
}
