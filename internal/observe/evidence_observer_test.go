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

	"gwentcoachmvp/internal/cards"
	"gwentcoachmvp/internal/rules"
	"gwentcoachmvp/internal/stateestimate"
	"gwentcoachmvp/internal/uiphase"
)

func TestNativeEvidenceObserverDoesNotUseStageView(t *testing.T) {
	cal, frames := evidenceFixture()
	reader, err := NewCalibratedEvidenceReader(cal)
	if err != nil {
		t.Fatal(err)
	}
	seq := 0
	scene := 0
	a := nativeLoopAdapter{cfg: NativeLoopConfig{MatchID: "m", States: map[string]LoopState{"turn": {View: rules.View{Score: [2]int{99, 99}}, Eligible: true}}}, evidenceReader: reader, capture: func(context.Context) (loopFrame, error) {
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

func TestEvidenceEstimateFeedsLocalLoop(t *testing.T) {
	cal, frames := evidenceFixture()
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
	a := nativeLoopAdapter{cfg: NativeLoopConfig{MatchID: "m"}, evidenceReader: reader, capture: func(ctx context.Context) (loopFrame, error) {
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
