package observe

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"path/filepath"
	"testing"
	"time"

	"gwentcoachmvp/internal/cardindex"
	"gwentcoachmvp/internal/cardmatch"
	"gwentcoachmvp/internal/cards"
	"gwentcoachmvp/internal/rules"
)

func TestNativeLoopTenRecognizedCardsSelectsSupportedSlot(t *testing.T) {
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
	keeper := state.Cards[0]
	state.Cards = nil
	img := image.NewGray(image.Rect(0, 0, 180, 160))
	var slots []cardmatch.ROI
	var order []string
	var points []image.Point
	for i := 0; i < 10; i++ {
		roi := cardmatch.ROI{X: i * 18, Y: 0, Width: 18, Height: 160}
		slots = append(slots, roi)
		points = append(points, image.Pt(100+i*50, 950))
		for y := 0; y < 160; y++ {
			for x := 0; x < 18; x++ {
				v := uint8(x * 12)
				if i == 6 {
					v = 255 - v
				}
				img.SetGray(i*18+x, y, color.Gray{Y: v})
			}
		}
		if i == 6 {
			keeper.ID = "keeper"
			state.Cards = append(state.Cards, keeper)
			order = append(order, keeper.ID)
		} else {
			c := catalog.Cards[112101]
			id := fmt.Sprintf("unknown-%d", i)
			state.Cards = append(state.Cards, rules.Instance{ID: id, CardID: 112101, Owner: 0, Power: c.Attributes.Power, Armor: c.Attributes.Armor, Row: -1, Zone: rules.Hand})
			order = append(order, id)
		}
	}
	first, err := cardmatch.Normalize(img, slots[0])
	if err != nil {
		t.Fatal(err)
	}
	keep, err := cardmatch.Normalize(img, slots[6])
	if err != nil {
		t.Fatal(err)
	}
	version := cardindex.Version{Data: cardindex.DataVersion, Rules: cardindex.RulesVersion}
	dbPath := filepath.Join(t.TempDir(), "cards.db")
	if err := cardindex.Import(dbPath, version, []string{"../../data/cards/14.10.0/en/cards.json"}); err != nil {
		t.Fatal(err)
	}
	db, err := cardindex.Open(dbPath, version)
	if err != nil {
		t.Fatal(err)
	}
	matcher, err := cardmatch.New(cardmatch.File{Version: version, FeatureVersion: cardmatch.FeatureVersion, Templates: []cardmatch.Template{{CardID: 112101, Feature: first}, {CardID: 202245, Feature: keep}}}, db)
	if err != nil {
		t.Fatal(err)
	}
	view, err := state.View(0)
	if err != nil {
		t.Fatal(err)
	}
	next := state
	next.Cards = append([]rules.Instance(nil), state.Cards...)
	next.Cards[6].Zone = rules.Board
	next.Cards[6].Row = 0
	next.Turn = 1
	next.Revision++
	after, err := next.View(0)
	if err != nil {
		t.Fatal(err)
	}
	// A new layout is required after removing the selected card.
	afterSlots := append([]cardmatch.ROI(nil), slots[:9]...)
	afterImage := image.NewGray(img.Bounds())
	for y := 0; y < 160; y++ {
		for x := 0; x < 180; x++ {
			afterImage.SetGray(x, y, color.Gray{Y: uint8((x % 18) * 12)})
		}
	}
	cfg := NativeLoopConfig{MatchID: "m", Hand: HandScanConfig{Width: 180, Height: 160, Slots: slots, MaxDistance: 0, MinGap: 1}, States: map[string]LoopState{
		"ready": {View: view, HandOrder: order, Eligible: true, CardPoints: points, SelectedPhases: map[string]string{"keeper": "selected"}, PlacedPhases: map[string]string{"keeper": "placed"}},
		"done":  {View: after, HandOrder: append(append([]string(nil), order[:6]...), order[7:]...), HandSlots: afterSlots, Result: true},
	}, RowPoints: [2]image.Point{image.Pt(900, 700), image.Pt(900, 800)}, EndTurnPoint: image.Pt(1845, 540)}
	phase := "ready"
	seq := 0
	var clicks []image.Point
	capture := func(ctx context.Context) (loopFrame, error) {
		if err := ctx.Err(); err != nil {
			return loopFrame{}, err
		}
		seq++
		frame := img
		if phase == "done" {
			frame = afterImage
		}
		return loopFrame{navFrame: navFrame{Phase: phase, Reference: fmt.Sprint(seq), Captured: time.Now(), Width: 1920, Height: 1080}, Image: frame}, nil
	}
	send := func(_ context.Context, _ loopFrame, _ string, p image.Point, _ time.Time) error {
		clicks = append(clicks, p)
		switch phase {
		case "ready":
			phase = "selected"
		case "selected":
			phase = "placed"
		case "placed":
			phase = "done"
		default:
			t.Fatalf("unexpected phase %s", phase)
		}
		return nil
	}
	adapter := &nativeLoopAdapter{cfg: cfg, matcher: matcher, capture: capture, send: send}
	service := rules.NewDecisionService(engine)
	defer service.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err = (LocalLoop{Observe: adapter.observe, Execute: adapter.execute, Confirm: adapter.confirm, Decisions: service, FilterCandidates: filterNativeCandidates, DecisionBudget: time.Second, ConfirmationBudget: time.Second}).Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(clicks) != 3 || clicks[0] != points[6] {
		t.Fatalf("wrong slot clicked: %v", clicks)
	}
}
