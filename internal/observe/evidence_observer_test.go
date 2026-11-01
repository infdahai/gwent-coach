package observe

import (
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"testing"
	"time"

	"gwentcoachmvp/internal/cardindex"
	"gwentcoachmvp/internal/cardmatch"
	"gwentcoachmvp/internal/cards"
	"gwentcoachmvp/internal/rules"
	"gwentcoachmvp/internal/stateestimate"
	"gwentcoachmvp/internal/uiphase"
	"path/filepath"
)

func evidenceHandMatcher(t *testing.T, frames [2]*image.RGBA) (HandScanConfig, *cardmatch.Index) {
	t.Helper()
	for _, img := range frames {
		for y := 8; y < 64; y++ {
			for x := 0; x < 192; x++ {
				img.Set(x, y, color.Gray{Y: uint8(x)})
			}
		}
	}
	roi := cardmatch.ROI{X: 0, Y: 8, Width: 192, Height: 56}
	feature, err := cardmatch.Normalize(frames[0], roi)
	if err != nil {
		t.Fatal(err)
	}
	reverse := image.NewGray(frames[0].Bounds())
	for y := 8; y < 64; y++ {
		for x := 0; x < 192; x++ {
			reverse.SetGray(x, y, color.Gray{Y: 255 - uint8(x)})
		}
	}
	other, err := cardmatch.Normalize(reverse, roi)
	if err != nil {
		t.Fatal(err)
	}
	version := cardindex.Version{Data: cardindex.DataVersion, Rules: cardindex.RulesVersion}
	path := filepath.Join(t.TempDir(), "cards.db")
	if err := cardindex.Import(path, version, []string{"../../data/cards/14.10.0/en/cards.json"}); err != nil {
		t.Fatal(err)
	}
	db, err := cardindex.Open(path, version)
	if err != nil {
		t.Fatal(err)
	}
	matcher, err := cardmatch.New(cardmatch.File{Version: version, FeatureVersion: cardmatch.FeatureVersion, Templates: []cardmatch.Template{{CardID: 152405, Feature: feature}, {CardID: 113310, Feature: other}}}, db)
	if err != nil {
		t.Fatal(err)
	}
	return HandScanConfig{Width: 192, Height: 64, Slots: []cardmatch.ROI{roi}, MinGap: 1}, matcher
}

func TestNativeEvidenceObserverDoesNotUseStageView(t *testing.T) {
	cal, frames := evidenceFixture()
	hand, matcher := evidenceHandMatcher(t, frames)
	reader, err := NewCalibratedEvidenceReader(cal)
	if err != nil {
		t.Fatal(err)
	}
	seq := 0
	scene := 0
	a := nativeLoopAdapter{matcher: matcher, cfg: NativeLoopConfig{Hand: hand, MatchID: "m", States: map[string]LoopState{"turn": {View: rules.View{Score: [2]int{99, 99}}, Eligible: true}}}, evidenceReader: reader, capture: func(context.Context) (loopFrame, error) {
		seq++
		return loopFrame{navFrame: navFrame{Phase: "turn", Reference: fmt.Sprint(seq), Captured: time.Now(), Width: 192, Height: 64}, Image: frames[scene]}, nil
	}}
	first, err := a.observe(context.Background())
	if err != nil || !first.Eligible || first.View.Score[1] != 0 {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	scene = 1
	second, err := a.observe(context.Background())
	if err != nil || second.View.Score[1] != 5 || second.StateKey == first.StateKey {
		t.Fatalf("second=%+v err=%v", second, err)
	}
	cal.Totals = nil
	reader, err = NewCalibratedEvidenceReader(cal)
	if err != nil {
		t.Fatal(err)
	}
	a.evidenceReader = reader
	if _, err = a.observe(context.Background()); !errors.Is(err, ErrUnstableVisual) {
		t.Fatalf("partial evidence used legacy state: %v", err)
	}
}

func TestHandEvidenceBridgeRejectsUnverifiedCards(t *testing.T) {
	cal, frames := evidenceFixture()
	hand, matcher := evidenceHandMatcher(t, frames)
	reader, err := NewCalibratedEvidenceReader(cal)
	if err != nil {
		t.Fatal(err)
	}
	pair, now := readEvidencePair(t, reader, frames[0])
	images := [2]HandFrame{{pair[0].Frame.ID, pair[0].Frame.CapturedAt, frames[0]}, {pair[1].Frame.ID, pair[1].Frame.CapturedAt, frames[0]}}
	if o, err := SemanticFromHandEvidence(pair, images, hand, matcher, now, time.Second); err != nil || !o.Eligible {
		t.Fatalf("verified: %+v %v", o, err)
	}
	if _, err := SemanticFromHandEvidence(pair, images, HandScanConfig{}, nil, now, time.Second); err == nil {
		t.Fatal("missing calibration accepted")
	}
	mismatch := images
	mismatch[0].Source = "other"
	if _, err := SemanticFromHandEvidence(pair, mismatch, hand, matcher, now, time.Second); err == nil {
		t.Fatal("unbound frame accepted")
	}
	changed := image.NewRGBA(frames[0].Bounds())
	draw.Draw(changed, changed.Bounds(), frames[0], image.Point{}, draw.Src)
	for y := 8; y < 64; y++ {
		for x := 0; x < 192; x++ {
			changed.Set(x, y, color.Gray{Y: 255 - uint8(x)})
		}
	}
	mismatch = images
	mismatch[1].Image = changed
	if _, err := SemanticFromHandEvidence(pair, mismatch, hand, matcher, now, time.Second); err == nil {
		t.Fatal("conflicting pixels accepted")
	}
	pair[0].Facts.Totals = nil
	if _, err := SemanticFromHandEvidence(pair, images, hand, matcher, now, time.Second); err == nil {
		t.Fatal("incomplete facts accepted")
	}
}

func TestPartialTerminalEvidenceIsReadOnly(t *testing.T) {
	cal, frames := evidenceFixture()
	cal.Totals = nil
	cal.Outcome = evidenceFact([]image.Image{frames[0]}, []string{"finished"}, []stateestimate.OutcomeFacts{{Finished: true, Winner: 1}}, 160)
	reader, err := NewCalibratedEvidenceReader(cal)
	if err != nil {
		t.Fatal(err)
	}
	pair, now := readEvidencePair(t, reader, frames[0])
	observation, err := stateestimate.Estimate(pair, now, time.Second)
	if err != nil || !observation.ReadyFor(stateestimate.TaskResult) || observation.Eligible || observation.View != nil {
		t.Fatalf("partial result: %+v, %v", observation, err)
	}
	semantic, err := SemanticFromEvidence(pair, now, time.Second)
	if err != nil || !semantic.Result || semantic.PartialResult == nil || semantic.Eligible {
		t.Fatalf("partial result mapping: %+v, %v", semantic, err)
	}
}

func TestInconsistentCompleteViewStillReportsIndependentResult(t *testing.T) {
	cal, frames := evidenceFixture()
	cal.Outcome = evidenceFact([]image.Image{frames[0]}, []string{"finished"}, []stateestimate.OutcomeFacts{{Finished: true, Winner: 1}}, 160)
	for key, totals := range cal.Totals.Values {
		totals.Score[0] = 999
		cal.Totals.Values[key] = totals
	}
	reader, err := NewCalibratedEvidenceReader(cal)
	if err != nil {
		t.Fatal(err)
	}
	seq := 0
	a := nativeLoopAdapter{cfg: NativeLoopConfig{MatchID: "m"}, evidenceReader: reader, capture: func(context.Context) (loopFrame, error) {
		seq++
		return loopFrame{navFrame: navFrame{Reference: fmt.Sprint(seq), Captured: time.Now(), Width: 192, Height: 64}, Image: frames[0]}, nil
	}}
	o, err := a.observe(context.Background())
	if err != nil || !o.Result || o.Eligible || o.PartialResult == nil || o.PartialResult.Winner != 1 {
		t.Fatalf("partial terminal observation: %+v, %v", o, err)
	}
	cal.Outcome = evidenceFact([]image.Image{frames[0]}, []string{"ongoing"}, []stateestimate.OutcomeFacts{{Winner: -1}}, 160)
	a.evidenceReader, err = NewCalibratedEvidenceReader(cal)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.observe(context.Background()); err == nil {
		t.Fatal("inconsistent decision view accepted")
	}
}

func TestEvidenceEstimateFeedsLocalLoop(t *testing.T) {
	cal, frames := evidenceFixture()
	hand, matcher := evidenceHandMatcher(t, frames)
	// Keep the independently labelled hand legal for this rules snapshot.
	for key, value := range cal.Cards.Values {
		value.Visible[0].Power = 5
		cal.Cards.Values[key] = value
	}
	terminal := image.NewRGBA(frames[0].Bounds())
	draw.Draw(terminal, terminal.Bounds(), frames[0], image.Point{}, draw.Src)
	draw.Draw(terminal, image.Rect(160, 0, 192, 8), image.NewUniform(color.White), image.Point{}, draw.Src)
	cal.Outcome = evidenceFact([]image.Image{frames[0], terminal}, []string{"ongoing", "finished"}, []stateestimate.OutcomeFacts{{Winner: -1}, {Finished: true, Winner: 1}}, 160)
	reader, err := NewCalibratedEvidenceReader(cal)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := cards.Load("../../data/cards/14.10.0/en/cards.json")
	if err != nil {
		t.Fatal(err)
	}
	engine, err := rules.New(catalog)
	if err != nil {
		t.Fatal(err)
	}
	stageReader, err := uiphase.NewRecognizer(cal.Stage, "m", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	seq, sends := 0, 0
	delivered := make(chan struct{})
	a := nativeLoopAdapter{matcher: matcher, cfg: NativeLoopConfig{MatchID: "m", Hand: hand}, evidenceReader: reader, capture: func(ctx context.Context) (loopFrame, error) {
		seq++
		var img image.Image = frames[0]
		if seq > 2 {
			select {
			case <-delivered:
			case <-ctx.Done():
				return loopFrame{}, ctx.Err()
			}
			img = terminal
			time.Sleep(time.Millisecond)
		}
		at := time.Now()
		id := fmt.Sprint(seq)
		stage, err := stageReader.Observe(uiphase.Frame{Ref: uiphase.FrameRef{ID: id, StreamID: "m", CapturedAt: at, Width: 192, Height: 64}, Image: img}, time.Now())
		if err != nil {
			return loopFrame{}, err
		}
		return loopFrame{navFrame: navFrame{Phase: stage.Evidence.Phase, Reference: id, Captured: at, Width: 192, Height: 64}, Image: img, Stage: &stage}, nil
	}}
	loop := LocalLoop{Decisions: rules.NewEvaluator(engine), DecisionBudget: time.Second, ConfirmationBudget: time.Second, Observe: a.observe,
		Execute: func(_ context.Context, s Snapshot, action rules.Action, _ time.Time) error {
			if action.Kind != "play" || s.Version != 1 {
				t.Errorf("action=%+v snapshot=%+v", action, s)
			}
			sends++
			close(delivered)
			return nil
		}, Confirm: func(_ rules.Action, o SemanticObservation) bool { return o.Result }}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err = loop.Run(ctx); err != nil || sends != 1 {
		t.Fatalf("err=%v sends=%d", err, sends)
	}
}
