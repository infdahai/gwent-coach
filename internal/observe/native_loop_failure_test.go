package observe

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"testing"
	"time"

	"gwentcoachmvp/internal/cardmatch"
	"gwentcoachmvp/internal/cards"
	"gwentcoachmvp/internal/rules"
)

func TestResumeSelectedDoesNotReplayCardClick(t *testing.T) {
	catalog, err := cards.Load("../../data/cards/14.10.0/en/cards.json")
	if err != nil {
		t.Fatal(err)
	}
	engine, err := rules.New(catalog)
	if err != nil {
		t.Fatal(err)
	}
	state, err := engine.Initial([2][]int{{202245}, {}}, [2][]int{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := state.View(0)
	action := rules.Action{Kind: "play", Card: state.Cards[0].ID}
	next, _, err := engine.Apply(state, action)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := next.View(0)
	phase, seq := "selected", 0
	var sent []image.Point
	cfg := NativeLoopConfig{MatchID: "m", ResumeBaseline: "turn", ResumeSelectedPhase: "selected", ResumeAction: &action, ResumeCard: action.Card, RowPoints: [2]image.Point{image.Pt(1100, 740), image.Pt(1100, 570)}, EndTurnPoint: image.Pt(1845, 540), States: map[string]LoopState{"turn": {View: before, HandOrder: []string{action.Card}, Eligible: true, PlacedPhases: map[string]string{action.Card: "placed"}}, "done": {View: after}}}
	adapter := &nativeLoopAdapter{cfg: cfg, capture: func(context.Context) (loopFrame, error) {
		seq++
		return loopFrame{navFrame: navFrame{Phase: phase, Reference: fmt.Sprint(seq), Captured: time.Now(), Width: 1920, Height: 1080}}, nil
	}, send: func(_ context.Context, _ loopFrame, _ string, p image.Point, _ time.Time) error {
		sent = append(sent, p)
		if len(sent) == 1 {
			phase = "placed"
		} else {
			phase = "done"
		}
		return nil
	}}
	service := rules.NewDecisionService(engine)
	defer service.Close()
	if err := adapter.resumeSelected(context.Background(), service); err != nil {
		t.Fatal(err)
	}
	if len(sent) != 2 || sent[0] != cfg.RowPoints[0] || sent[1] != cfg.EndTurnPoint {
		t.Fatalf("unexpected replay: %v", sent)
	}
}

func TestResumePlacedRejectsNonWolfWithoutInput(t *testing.T) {
	catalog, err := cards.Load("../../data/cards/14.10.0/en/cards.json")
	if err != nil {
		t.Fatal(err)
	}
	engine, err := rules.New(catalog)
	if err != nil {
		t.Fatal(err)
	}
	state, err := engine.Initial([2][]int{{202245}, {}}, [2][]int{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	view, _ := state.View(0)
	id := state.Cards[0].ID
	seq, sends := 0, 0
	cfg := NativeLoopConfig{MatchID: "m", ResumeBaseline: "turn", ResumePlacedPhase: "placed", ResumeCard: id, RowPoints: [2]image.Point{image.Pt(1100, 740)}, EndTurnPoint: image.Pt(1845, 540), States: map[string]LoopState{"turn": {View: view, HandOrder: []string{id}, Eligible: true, PlacedPhases: map[string]string{id: "placed"}}}}
	a := nativeLoopAdapter{cfg: cfg, capture: func(context.Context) (loopFrame, error) {
		seq++
		return loopFrame{navFrame: navFrame{Phase: "placed", Reference: fmt.Sprint(seq), Captured: time.Now()}}, nil
	}, send: func(context.Context, loopFrame, string, image.Point, time.Time) error { sends++; return nil }}
	service := rules.NewDecisionService(engine)
	defer service.Close()
	if err := a.resumeSelected(context.Background(), service); err == nil || sends != 0 {
		t.Fatalf("err=%v sends=%d", err, sends)
	}
}

func TestResumeOrderSelectedClicksOnlyVerifiedTarget(t *testing.T) {
	catalog, err := cards.Load("../../data/cards/14.10.0/en/cards.json")
	if err != nil {
		t.Fatal(err)
	}
	engine, err := rules.New(catalog)
	if err != nil {
		t.Fatal(err)
	}
	state, err := engine.Initial([2][]int{{132403, 202140}, {}}, [2][]int{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	state.Cards[0].Zone, state.Cards[0].Row = rules.Board, 0
	state.Cards[1].Zone, state.Cards[1].Row = rules.Board, 1
	before, _ := state.View(0)
	action := rules.Action{Kind: "order", Card: state.Cards[1].ID, Target: state.Cards[0].ID}
	next, _, err := engine.Apply(state, action)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := next.View(0)
	phase, seq := "order-selected", 0
	var sent []image.Point
	cfg := NativeLoopConfig{MatchID: "m", ResumeBaseline: "turn", ResumeOrderPhase: phase, ResumeAction: &action, ResumeOrderCard: action.Card, ResumeOrderTarget: action.Target, OrderTargets: map[string]image.Point{action.Target: image.Pt(960, 770)}, States: map[string]LoopState{"turn": {View: before, Eligible: true}, "done": {View: after}}}
	a := nativeLoopAdapter{cfg: cfg, capture: func(context.Context) (loopFrame, error) {
		seq++
		return loopFrame{navFrame: navFrame{Phase: phase, Reference: fmt.Sprint(seq), Captured: time.Now(), Width: 1920, Height: 1080}}, nil
	}, send: func(_ context.Context, _ loopFrame, _ string, p image.Point, _ time.Time) error {
		sent = append(sent, p)
		phase = "done"
		return nil
	}}
	service := rules.NewDecisionService(engine)
	defer service.Close()
	if err := a.resumeOrderSelected(context.Background(), service); err != nil || len(sent) != 1 || sent[0] != cfg.OrderTargets[action.Target] {
		t.Fatalf("err=%v sent=%v", err, sent)
	}
}

func TestPassIsConfirmedByRecognizedMatchResult(t *testing.T) {
	a := nativeLoopAdapter{before: SemanticObservation{View: rules.View{Round: 2}}}
	if !a.confirm(rules.Action{Kind: "pass"}, SemanticObservation{Player: 0, Result: true}) {
		t.Fatal("recognized result must confirm pending pass")
	}
	a.before.View.Passed[0] = true
	if a.confirm(rules.Action{Kind: "pass"}, SemanticObservation{Player: 0, Result: true}) {
		t.Fatal("cannot confirm a duplicate pass")
	}
}

func TestPlayRecognizesAutomaticTurnTransitionWithoutEndClick(t *testing.T) {
	catalog, err := cards.Load("../../data/cards/14.10.0/en/cards.json")
	if err != nil {
		t.Fatal(err)
	}
	engine, err := rules.New(catalog)
	if err != nil {
		t.Fatal(err)
	}
	state, err := engine.Initial([2][]int{{202245}, {}}, [2][]int{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := state.View(0)
	action := rules.Action{Kind: "play", Card: state.Cards[0].ID}
	next, _, err := engine.Apply(state, action)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := next.View(0)
	phase, seq := "selected", 0
	var sent []image.Point
	ls := LoopState{View: before, HandOrder: []string{action.Card}, Eligible: true, AutoEndedPhases: map[string]string{action.Card: "ended"}}
	cfg := NativeLoopConfig{MatchID: "m", RowPoints: [2]image.Point{image.Pt(1100, 740)}, EndTurnPoint: image.Pt(1845, 540), States: map[string]LoopState{"ended": {View: after}}}
	a := nativeLoopAdapter{cfg: cfg, state: ls, before: SemanticObservation{MatchID: "m", Player: 0, View: before, HandOrder: ls.HandOrder}, capture: func(context.Context) (loopFrame, error) {
		seq++
		return loopFrame{navFrame: navFrame{Phase: phase, Reference: fmt.Sprint(seq), Captured: time.Now(), Width: 1920, Height: 1080}}, nil
	}, send: func(_ context.Context, _ loopFrame, _ string, p image.Point, _ time.Time) error {
		sent = append(sent, p)
		phase = "ended"
		return nil
	}}
	if err := a.finishPlay(context.Background(), loopFrame{}, action, "", time.Now().Add(time.Second)); err != nil || len(sent) != 1 || sent[0] != cfg.RowPoints[0] {
		t.Fatalf("err=%v sent=%v", err, sent)
	}
}

func TestResumeMageTargetDoesNotReplayCardOrRow(t *testing.T) {
	catalog, err := cards.Load("../../data/cards/14.10.0/en/cards.json")
	if err != nil {
		t.Fatal(err)
	}
	engine, err := rules.New(catalog)
	if err != nil {
		t.Fatal(err)
	}
	state, err := engine.Initial([2][]int{{202908, 132403}, {200535}}, [2][]int{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	state.Cards[1].Zone, state.Cards[1].Row, state.Cards[1].Power = rules.Board, 0, 6
	state.Cards[2].Zone, state.Cards[2].Row, state.Cards[2].Power = rules.Board, 1, 5
	view, _ := state.View(0)
	card, target := state.Cards[0].ID, state.Cards[2].ID
	phase, seq := "target", 0
	var sent []image.Point
	cfg := NativeLoopConfig{MatchID: "m", ResumeBaseline: "turn", ResumeTargetPhase: phase, ResumeAction: &rules.Action{Kind: "play", Card: card, Target: target}, ResumeTargetCard: card, ResumeTargetID: target, TargetPoints: map[string]image.Point{target: image.Pt(960, 370)}, States: map[string]LoopState{"turn": {View: view, HandOrder: []string{card}, Eligible: true}}}
	a := nativeLoopAdapter{cfg: cfg, capture: func(context.Context) (loopFrame, error) {
		seq++
		return loopFrame{navFrame: navFrame{Phase: phase, Reference: fmt.Sprint(seq), Captured: time.Now(), Width: 1920, Height: 1080}}, nil
	}, send: func(_ context.Context, _ loopFrame, _ string, p image.Point, _ time.Time) error {
		sent = append(sent, p)
		phase = "unknown"
		return nil
	}}
	service := rules.NewDecisionService(engine)
	defer service.Close()
	err = a.resumeMageTarget(context.Background(), service)
	if err == nil || len(sent) != 1 || sent[0] != cfg.TargetPoints[target] {
		t.Fatalf("err=%v sent=%v", err, sent)
	}
}

func TestResumePlacedMageConfirmsDamageBeforeEnding(t *testing.T) {
	catalog, err := cards.Load("../../data/cards/14.10.0/en/cards.json")
	if err != nil {
		t.Fatal(err)
	}
	engine, err := rules.New(catalog)
	if err != nil {
		t.Fatal(err)
	}
	state, err := engine.Initial([2][]int{{202908, 132403}, {200535}}, [2][]int{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	state.Cards[1].Zone, state.Cards[1].Row, state.Cards[1].Power = rules.Board, 0, 6
	state.Cards[2].Zone, state.Cards[2].Row, state.Cards[2].Power = rules.Board, 1, 5
	before, _ := state.View(0)
	card, target := state.Cards[0].ID, state.Cards[2].ID
	action := rules.Action{Kind: "play", Card: card, Target: target}
	next, _, err := engine.Apply(state, action)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := next.View(0)
	preEnd := after
	preEnd.Turn = 0
	img := image.NewRGBA(image.Rect(0, 0, 1920, 1080))
	roi := cardmatch.ROI{X: 10, Y: 10, Width: 32, Height: 32}
	anchor := Anchor{X: roi.X, Y: roi.Y, Width: roi.Width, Height: roi.Height, MaxError: .08}
	anchor.Pixels = AnchorPixels(img, anchor, 1920, 1080)
	model := &PhaseModel{Width: 1920, Height: 1080, Phases: []Phase{{Name: "placed", Anchors: []Anchor{anchor, anchor}}}}
	phase, seq := "placed", 0
	var sent []image.Point
	cfg := NativeLoopConfig{MatchID: "m", ResumeBaseline: "turn", ResumePlacedPhase: "placed", ResumeAction: &action, ResumeCard: card, PlacedIdentityByPhase: true, PlacedArt: map[string]cardmatch.ROI{card: roi}, RowPoints: [2]image.Point{image.Pt(1100, 740)}, EndTurnPoint: image.Pt(1845, 540), States: map[string]LoopState{"turn": {View: before, HandOrder: []string{card}, Eligible: true, PlacedPhases: map[string]string{card: "placed"}}, "placed": {View: preEnd}, "done": {View: after}}}
	a := nativeLoopAdapter{cfg: cfg, model: model, capture: func(context.Context) (loopFrame, error) {
		seq++
		return loopFrame{navFrame: navFrame{Phase: phase, Reference: fmt.Sprint(seq), Captured: time.Now(), Width: 1920, Height: 1080}, Image: img}, nil
	}, send: func(_ context.Context, _ loopFrame, _ string, p image.Point, _ time.Time) error {
		sent = append(sent, p)
		phase = "done"
		return nil
	}}
	service := rules.NewDecisionService(engine)
	defer service.Close()
	if err := a.resumeSelected(context.Background(), service); err != nil || len(sent) != 1 || sent[0] != cfg.EndTurnPoint {
		t.Fatalf("err=%v sent=%v", err, sent)
	}
}

func TestPlacedPhaseIdentityRequiresBoundArtAndIndependentFrames(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 1920, 1080))
	roi := cardmatch.ROI{X: 10, Y: 10, Width: 32, Height: 32}
	art := Anchor{X: roi.X, Y: roi.Y, Width: roi.Width, Height: roi.Height, MaxError: 0.08}
	art.Pixels = AnchorPixels(img, art, 1920, 1080)
	other := Anchor{X: 50, Y: 50, Width: 32, Height: 32, MaxError: 0.08}
	other.Pixels = AnchorPixels(img, other, 1920, 1080)
	model := &PhaseModel{Width: 1920, Height: 1080, Phases: []Phase{{Name: "placed", Anchors: []Anchor{art, other}}}}
	a := nativeLoopAdapter{cfg: NativeLoopConfig{PlacedIdentityByPhase: true, PlacedArt: map[string]cardmatch.ROI{"wolf": roi}}, model: model, before: SemanticObservation{View: rules.View{Visible: []rules.Instance{{ID: "wolf", CardID: 132403, Owner: 0, Zone: rules.Hand}}}}}
	f := loopFrame{navFrame: navFrame{Reference: "one"}, Image: img}
	g := loopFrame{navFrame: navFrame{Reference: "two"}, Image: img}
	if err := a.verifyPlacedWolf(rules.Action{Card: "wolf"}, f, g, "placed"); err != nil {
		t.Fatal(err)
	}
	if err := a.verifyPlacedWolf(rules.Action{Card: "wolf"}, f, f, "placed"); err == nil {
		t.Fatal("accepted reused frame")
	}
	variantFrame := image.NewRGBA(img.Bounds())
	draw.Draw(variantFrame, image.Rect(roi.X, roi.Y, roi.X+roi.Width, roi.Y+roi.Height), image.NewUniform(color.White), image.Point{}, draw.Src)
	a.model.Phases[0].Variants = [][]Anchor{{other, other}}
	v := loopFrame{navFrame: navFrame{Reference: "variant"}, Image: variantFrame}
	if phase, _ := a.model.Recognize(variantFrame); phase != "placed" {
		t.Fatalf("variant did not match: %s", phase)
	}
	if err := a.verifyPlacedWolf(rules.Action{Card: "wolf"}, f, v, "placed"); err == nil {
		t.Fatal("accepted a variant without matching identity art")
	}
	a.model.Phases[0].Anchors = []Anchor{other, other}
	if err := a.verifyPlacedWolf(rules.Action{Card: "wolf"}, f, g, "placed"); err == nil {
		t.Fatal("accepted missing art anchor")
	}
}

func TestNativeLoopMulliganTransition(t *testing.T) {
	phase, seq, sends := "mulligan", 0, 0
	cfg := NativeLoopConfig{MatchID: "m", MulliganPhase: "mulligan", MulliganConfirmPhase: "confirm", FinishMulliganPoint: image.Pt(870, 1020), ConfirmMulliganPoint: image.Pt(892, 635), States: map[string]LoopState{"mulligan": {}}}
	adapter := &nativeLoopAdapter{cfg: cfg, capture: func(context.Context) (loopFrame, error) {
		seq++
		return loopFrame{navFrame: navFrame{Phase: phase, Reference: fmt.Sprint(seq), Captured: time.Now(), Width: 1920, Height: 1080}}, nil
	}, send: func(_ context.Context, _ loopFrame, kind string, p image.Point, _ time.Time) error {
		sends++
		if kind != "click" {
			t.Errorf("unexpected action %s", kind)
		}
		if sends == 1 && p == cfg.FinishMulliganPoint {
			phase = "confirm"
			return nil
		}
		if sends == 2 && p == cfg.ConfirmMulliganPoint {
			phase = "turn"
			return nil
		}
		return fmt.Errorf("unexpected input")
	}}
	if err := adapter.finishMulligan(context.Background()); err != nil || sends != 2 {
		t.Fatalf("err=%v sends=%d", err, sends)
	}
}

func TestNativePlannerRejectsOtherPlayer(t *testing.T) {
	planner := ActionPlanner{}
	observation := SemanticObservation{Player: 0}
	if got := planner.Candidates(observation, []rules.Action{{Kind: "pass", Player: 1}}); len(got) != 0 {
		t.Fatalf("other player's action accepted: %v", got)
	}
}

func TestNativeLoopWolfPackDecisionAndConfirmation(t *testing.T) {
	before := rules.View{HandCount: [2]int{1, 0}, Score: [2]int{5, 0}, Visible: []rules.Instance{
		{ID: "wolf", CardID: 132403, Owner: 0, Power: 1, Row: -1, Zone: rules.Hand},
		{ID: "ally", CardID: 152405, Owner: 0, Power: 5, Row: 0, Zone: rules.Board},
	}}
	action := rules.Action{Kind: "play", Card: "wolf", Row: 0}
	obs := SemanticObservation{Player: 0, View: before, HandOrder: []string{"wolf"}}
	if got := filterNativeCandidates(obs, []rules.Action{action}); len(got) != 1 {
		t.Fatalf("wolf excluded: %v", got)
	}
	adapter := nativeLoopAdapter{before: obs}
	after := SemanticObservation{Player: 0, View: rules.View{HandCount: [2]int{0, 0}, Score: [2]int{7, 0}, Visible: []rules.Instance{{ID: "wolf", CardID: 132403, Owner: 0, Power: 2, Row: 0, Zone: rules.Board}}}}
	if !adapter.confirm(action, after) {
		t.Fatal("wolf boost outcome not confirmed")
	}
	after.View.Score[0] = 6
	if adapter.confirm(action, after) {
		t.Fatal("accepted incorrect wolf score")
	}
}

func TestMageConfirmationRequiresDamageAndDeployment(t *testing.T) {
	before := rules.View{HandCount: [2]int{1, 0}, Score: [2]int{6, 5}, Visible: []rules.Instance{{ID: "mage", CardID: 202908, Owner: 0, Power: 1, Zone: rules.Hand, Row: -1}, {ID: "enemy", CardID: 200535, Owner: 1, Power: 5, Zone: rules.Board, Row: 1}}}
	a := nativeLoopAdapter{before: SemanticObservation{Player: 0, View: before}}
	action := rules.Action{Kind: "play", Card: "mage", Target: "enemy", Row: 0}
	after := SemanticObservation{Player: 0, View: rules.View{HandCount: [2]int{0, 0}, Score: [2]int{7, 3}, Visible: []rules.Instance{{ID: "mage", CardID: 202908, Owner: 0, Power: 1, Zone: rules.Board, Row: 0}, {ID: "enemy", CardID: 200535, Owner: 1, Power: 3, Zone: rules.Board, Row: 1}}}}
	if !a.confirm(action, after) {
		t.Fatal("verified mage deployment rejected")
	}
	after.View.Visible[1].Power = 4
	if a.confirm(action, after) {
		t.Fatal("accepted wrong enemy power")
	}
}

func TestNativeLoopRejectsUnknownOrChangedState(t *testing.T) {
	n := 0
	capture := func(context.Context) (loopFrame, error) {
		n++
		return loopFrame{navFrame: navFrame{Phase: "unknown", Reference: time.Now().String(), Captured: time.Now(), Width: 1920, Height: 1080}}, nil
	}
	adapter := &nativeLoopAdapter{cfg: NativeLoopConfig{MatchID: "m", States: map[string]LoopState{}}, capture: capture, send: func(context.Context, loopFrame, string, image.Point, time.Time) error {
		t.Fatal("unexpected input")
		return nil
	}}
	if _, err := adapter.observe(context.Background()); err == nil {
		t.Fatal("accepted unknown state")
	}
	adapter.cfg.States["unknown"] = LoopState{View: rules.View{Ruleset: "native-v1", Round: 1, Winner: -1}, Eligible: true}
	old := SemanticObservation{Source: "earlier", MatchID: "m", Phase: "turn", Player: 0, View: rules.View{Ruleset: "native-v1", Round: 1, Winner: -1, Score: [2]int{2, 0}}, Eligible: true}
	encoded, err := semanticState(old)
	if err != nil {
		t.Fatal(err)
	}
	if err := adapter.execute(context.Background(), Snapshot{Source: "earlier", Phase: "turn", State: encoded}, rules.Action{Kind: "pass"}, time.Now().Add(time.Second)); err == nil {
		t.Fatal("accepted changed state")
	}
}
