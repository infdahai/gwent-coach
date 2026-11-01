package observe

import (
	"image"
	"image/color"
	"path/filepath"
	"testing"
	"time"

	"gwentcoachmvp/internal/cards"
	"gwentcoachmvp/internal/rules"

	"gwentcoachmvp/internal/cardindex"
	"gwentcoachmvp/internal/cardmatch"
)

func TestScanHandTwoFrames(t *testing.T) {
	version := cardindex.Version{Data: cardindex.DataVersion, Rules: cardindex.RulesVersion}
	dbPath := filepath.Join(t.TempDir(), "cards.db")
	if err := cardindex.Import(dbPath, version, []string{"../../data/cards/14.10.0/en/cards.json"}); err != nil {
		t.Fatal(err)
	}
	db, err := cardindex.Open(dbPath, version)
	if err != nil {
		t.Fatal(err)
	}
	img := image.NewGray(image.Rect(0, 0, 180, 160))
	reversed := image.NewGray(img.Bounds())
	for y := 0; y < 160; y++ {
		for x := 0; x < 180; x++ {
			img.SetGray(x, y, color.Gray{Y: uint8(x)})
			reversed.SetGray(x, y, color.Gray{Y: 255 - uint8(x)})
		}
	}
	roi := cardmatch.ROI{X: 0, Y: 0, Width: 180, Height: 160}
	first, err := cardmatch.Normalize(img, roi)
	if err != nil {
		t.Fatal(err)
	}
	second, err := cardmatch.Normalize(reversed, roi)
	if err != nil {
		t.Fatal(err)
	}
	matcher, err := cardmatch.New(cardmatch.File{Version: version, FeatureVersion: cardmatch.FeatureVersion, Templates: []cardmatch.Template{{CardID: 152405, Feature: first}, {CardID: 113310, Feature: second}}}, db)
	if err != nil {
		t.Fatal(err)
	}
	cfg := HandScanConfig{Width: 180, Height: 160, Slots: []cardmatch.ROI{roi}, MaxDistance: 0, MinGap: 1}
	got, err := ScanHand(cfg, matcher, [2]image.Image{img, img})
	if err != nil || len(got) != 1 || got[0].Status != "candidate" || got[0].CardID != 152405 {
		t.Fatalf("%+v %v", got, err)
	}
	got, err = ScanHand(cfg, matcher, [2]image.Image{img, reversed})
	if err != nil || got[0].Status != "unknown" || got[0].CardID != 0 {
		t.Fatalf("%+v %v", got, err)
	}
	catalog, err := cards.Load("../../data/cards/14.10.0/en/cards.json")
	if err != nil {
		t.Fatal(err)
	}
	engine, err := rules.New(catalog)
	if err != nil {
		t.Fatal(err)
	}
	state, err := engine.Initial([2][]int{{152405}, {}}, [2][]int{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	view, err := state.View(0)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	frames := [2]HandFrame{{Source: "first", CapturedAt: now, Image: img}, {Source: "second", CapturedAt: now, Image: img}}
	observation := SemanticObservation{Source: "second", CapturedAt: now, MatchID: "m", Phase: "turn", Eligible: true, View: view, HandOrder: []string{state.Cards[0].ID}}
	if err := VerifyObservedHand(cfg, matcher, frames, observation); err != nil {
		t.Fatal(err)
	}
	service := rules.NewDecisionService(engine)
	defer service.Close()
	input := rules.DecisionInput{MatchID: observation.MatchID, Phase: observation.Phase, View: view, HandOrder: observation.HandOrder, Deadline: time.Now().Add(time.Second)}
	input.Candidates, err = service.Candidates(input)
	if err != nil {
		t.Fatal(err)
	}
	action, err := service.Fast(input)
	if err != nil || action.Card != state.Cards[0].ID {
		t.Fatalf("decision=%+v err=%v", action, err)
	}
	unknown := observation
	unknown.View.Visible = append([]rules.Instance(nil), observation.View.Visible...)
	unknown.View.Visible[0].CardID = 0
	unknown.View.Visible[0].Power = 0
	unknown.View.Visible[0].Armor = 0
	if err := VerifyObservedHand(cfg, matcher, frames, unknown); err != nil {
		t.Fatalf("unknown slot rejected: %v", err)
	}
	unknownInput := input
	unknownInput.View = unknown.View
	unknownInput.Candidates, err = service.Candidates(unknownInput)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Fast(unknownInput); err == nil {
		t.Fatal("unknown card authorized a decision")
	}
	frames[1].Image = reversed
	if err := VerifyObservedHand(cfg, matcher, frames, observation); err == nil {
		t.Fatal("accepted conflicting frames")
	}
	cfg.Width++
	if _, err := ScanHand(cfg, matcher, [2]image.Image{img, img}); err == nil {
		t.Fatal("accepted wrong dimensions")
	}
}
