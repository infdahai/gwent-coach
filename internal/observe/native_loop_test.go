package observe

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"gwentcoachmvp/internal/cardindex"
	"gwentcoachmvp/internal/cardmatch"
	"gwentcoachmvp/internal/cards"
	"gwentcoachmvp/internal/rules"
)

func TestNativeLoopRecognizeDecideExecuteConfirm(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "cards.db")
	v := cardindex.Version{Data: cardindex.DataVersion, Rules: cardindex.RulesVersion}
	if err := cardindex.Import(dbPath, v, []string{"../../data/cards/14.10.0/en/cards.json"}); err != nil {
		t.Fatal(err)
	}
	db, err := cardindex.Open(dbPath, v)
	if err != nil {
		t.Fatal(err)
	}
	img := image.NewGray(image.Rect(0, 0, 180, 160))
	for y := 0; y < 160; y++ {
		for x := 0; x < 180; x++ {
			img.SetGray(x, y, color.Gray{Y: uint8(x)})
		}
	}
	roi := cardmatch.ROI{X: 0, Y: 0, Width: 180, Height: 160}
	feat, err := cardmatch.Normalize(img, roi)
	if err != nil {
		t.Fatal(err)
	}
	other := feat
	for i := range other.Gray {
		other.Gray[i] = 255 - other.Gray[i]
	}
	matcher, err := cardmatch.New(cardmatch.File{Version: v, FeatureVersion: cardmatch.FeatureVersion, Templates: []cardmatch.Template{{CardID: 202245, Feature: feat}, {CardID: 152405, Feature: other}}}, db)
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
	for _, scenario := range []struct{ pass, continuous bool }{{false, false}, {true, false}, {false, true}} {
		pass, continuous := scenario.pass, scenario.continuous
		t.Run(fmt.Sprintf("pass=%v/continuous=%v", pass, continuous), func(t *testing.T) {
			hand := []int{202245}
			if pass {
				hand = nil
			}
			state, err := engine.Initial([2][]int{hand, {}}, [2][]int{}, 0)
			if err != nil {
				t.Fatal(err)
			}
			view, err := state.View(0)
			if err != nil {
				t.Fatal(err)
			}
			action := rules.Action{Kind: "pass"}
			order := []string(nil)
			if !pass {
				action = rules.Action{Kind: "play", Card: state.Cards[0].ID}
				order = []string{action.Card}
			}
			after, _, err := engine.Apply(state, action)
			if err != nil {
				t.Fatal(err)
			}
			afterView, err := after.View(0)
			if err != nil {
				t.Fatal(err)
			}
			cfg := NativeLoopConfig{MatchID: "test", Hand: HandScanConfig{Width: 180, Height: 160, Slots: []cardmatch.ROI{roi}, MaxDistance: 0, MinGap: 1}, States: map[string]LoopState{"ready": {View: view, HandOrder: order, Eligible: true}, "done": {View: afterView, Result: true}}, SelectedPhase: "selected", PlacedPhase: "placed", CardPoints: []image.Point{{X: 10, Y: 10}}, RowPoints: [2]image.Point{{X: 20, Y: 20}, {X: 30, Y: 30}}, PassPoint: image.Pt(40, 40), EndTurnPoint: image.Pt(50, 50)}
			if continuous {
				opponentPassed, _, err := engine.Apply(after, rules.Action{Kind: "pass", Player: 1})
				if err != nil {
					t.Fatal(err)
				}
				nextView, err := opponentPassed.View(0)
				if err != nil {
					t.Fatal(err)
				}
				finished, _, err := engine.Apply(opponentPassed, rules.Action{Kind: "pass", Player: 0})
				if err != nil {
					t.Fatal(err)
				}
				finalView, err := finished.View(0)
				if err != nil {
					t.Fatal(err)
				}
				cfg.States["done"] = LoopState{View: afterView}
				cfg.States["next"] = LoopState{View: nextView, Eligible: true}
				cfg.States["final"] = LoopState{View: finalView, Result: true}
			}
			var mu sync.Mutex
			phase := "ready"
			postFrames := 0
			seq := 0
			var sent []string
			capture := func(ctx context.Context) (loopFrame, error) {
				if err := ctx.Err(); err != nil {
					return loopFrame{}, err
				}
				mu.Lock()
				defer mu.Unlock()
				seq++
				if continuous && phase == "done" {
					postFrames++
					if postFrames > 6 {
						phase = "next"
					}
				}
				return loopFrame{navFrame: navFrame{Phase: phase, Reference: fmt.Sprint(seq), Captured: time.Now(), Width: 1920, Height: 1080}, Image: img}, nil
			}
			send := func(_ context.Context, f loopFrame, kind string, p image.Point, _ time.Time) error {
				mu.Lock()
				defer mu.Unlock()
				sent = append(sent, kind)
				switch phase {
				case "ready":
					if pass && (kind != "hold" || p != cfg.PassPoint) || !pass && (kind != "click" || p != cfg.CardPoints[0]) {
						t.Errorf("incorrect first input: %s %v", kind, p)
					}
					if pass {
						phase = "done"
					} else {
						phase = "selected"
					}
				case "selected":
					if kind != "click" || p != cfg.RowPoints[0] {
						t.Errorf("incorrect row input: %s %v", kind, p)
					}
					phase = "placed"
				case "placed":
					if kind != "click" || p != cfg.EndTurnPoint {
						t.Errorf("incorrect end turn input: %s %v", kind, p)
					}
					phase = "done"
				case "next":
					if kind != "hold" || p != cfg.PassPoint {
						t.Errorf("incorrect pass input: %s %v", kind, p)
					}
					phase = "final"
				default:
					t.Errorf("unexpected input in %s", phase)
				}
				return nil
			}
			adapter := &nativeLoopAdapter{cfg: cfg, matcher: matcher, capture: capture, send: send}
			service := rules.NewDecisionService(engine)
			defer service.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			err = (LocalLoop{Observe: adapter.observe, Execute: adapter.execute, Confirm: adapter.confirm, Decisions: service, DecisionBudget: time.Second, ConfirmationBudget: time.Second}).Run(ctx)
			if err != nil {
				t.Fatal(err)
			}
			expected := 3
			if pass {
				expected = 1
			} else if continuous {
				expected = 4
			}
			if len(sent) != expected {
				t.Fatalf("sent=%v", sent)
			}
		})
	}
}
