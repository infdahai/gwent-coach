package observe

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gwentcoachmvp/internal/cards"
	"gwentcoachmvp/internal/matchlog"
	"gwentcoachmvp/internal/rules"
	"gwentcoachmvp/internal/stateestimate"
	"gwentcoachmvp/internal/uiphase"
)

type offlineSessionHost struct{ session TrainingSession }

func (h offlineSessionHost) ObserveStage(ctx context.Context, id string) (uiphase.Event, error) {
	return h.session.ObserveStage(ctx, id)
}
func (h offlineSessionHost) ExecuteInput(ctx context.Context, a Action) error {
	return h.session.ExecuteInput(ctx, a)
}
func (h offlineSessionHost) RunMatch(ctx context.Context, id string, j *ActionJournal) (SemanticObservation, error) {
	return h.session.RunMatch(ctx, id, j)
}

func TestSessionRecordsPartialVisualResult(t *testing.T) {
	cal, frames := evidenceFixture()
	cal.Totals = nil
	cal.Outcome = evidenceFact([]image.Image{frames[0]}, []string{"finished"}, []stateestimate.OutcomeFacts{{Finished: true, Winner: 1}}, 160)
	reader, err := NewCalibratedEvidenceReader(cal)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	inputs := 0
	s := TrainingSession{ID: "partial", LogRoot: root, StartMatch: 1, Matches: 1, TransitionBudget: time.Second,
		ObserveStage: func(context.Context, string) (uiphase.Event, error) {
			t.Fatal("unexpected navigation")
			return uiphase.Event{}, nil
		},
		ExecuteInput: func(context.Context, Action) error { inputs++; return nil },
		RunMatch: func(ctx context.Context, id string, j *ActionJournal) (SemanticObservation, error) {
			seq := 0
			a := nativeLoopAdapter{cfg: NativeLoopConfig{MatchID: id}, evidenceReader: reader, capture: func(context.Context) (loopFrame, error) {
				seq++
				return loopFrame{navFrame: navFrame{Reference: fmt.Sprint(seq), Captured: time.Now(), Width: 192, Height: 64}, Image: frames[0]}, nil
			}}
			var result SemanticObservation
			loop := LocalLoop{Observe: a.observe, Decisions: boundaryEvaluator{}, Journal: j, DecisionBudget: time.Second, ConfirmationBudget: time.Second,
				Execute: func(context.Context, Snapshot, rules.Action, time.Time) error { inputs++; return nil },
				Confirm: func(rules.Action, SemanticObservation) bool { t.Fatal("unexpected confirmation"); return false }, OnResult: func(o SemanticObservation) { result = o }}
			err := loop.Run(ctx)
			return result, err
		}}
	if err := s.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	events, err := matchlog.Read(filepath.Join(root, "partial-000001", "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if inputs != 0 || len(events) != 3 || events[1].EventType != "result_observed" || events[1].Result == nil || *events[1].Result != "loss" || events[2].EventType != "match_completed" || len(events[1].ConfirmationFrames) != 2 {
		t.Fatalf("inputs=%d events=%+v", inputs, events)
	}
}

func TestPartialResultRequiresTwoBoundFrames(t *testing.T) {
	now := time.Now()
	for _, frames := range [][]matchlog.FrameEvidence{
		nil,
		{{Source: "same", MatchID: "m", CapturedAt: now.Add(-time.Millisecond)}, {Source: "same", MatchID: "m", CapturedAt: now}},
		{{Source: "first", MatchID: "other", CapturedAt: now.Add(-time.Millisecond)}, {Source: "result", MatchID: "m", CapturedAt: now}},
	} {
		called := false
		o := SemanticObservation{Source: "result", MatchID: "m", Phase: "result", CapturedAt: now, CalibrationID: "cal", Frames: frames, Result: true, PartialResult: &stateestimate.OutcomeFacts{Finished: true, Winner: 0}}
		loop := LocalLoop{Decisions: boundaryEvaluator{}, DecisionBudget: time.Second, ConfirmationBudget: time.Second,
			Observe: func(context.Context) (SemanticObservation, error) { return o, nil },
			Execute: func(context.Context, Snapshot, rules.Action, time.Time) error {
				t.Fatal("unexpected input")
				return nil
			},
			Confirm: func(rules.Action, SemanticObservation) bool { return false }, OnResult: func(SemanticObservation) { called = true }}
		if err := loop.Run(context.Background()); err == nil || called {
			t.Fatalf("invalid frames accepted: %+v, %v", frames, err)
		}
	}
}

type partialResultEvaluator struct{ action rules.Action }

func (e partialResultEvaluator) Candidates(rules.DecisionInput) ([]rules.Action, error) {
	return []rules.Action{e.action}, nil
}
func (partialResultEvaluator) ValidateAction(rules.DecisionInput, rules.Action) error { return nil }

func TestPartialResultDoesNotConfirmPendingAction(t *testing.T) {
	sink := &receiptSink{}
	j := NewActionJournal(sink)
	resultFrame := make(chan SemanticObservation, 1)
	first := true
	confirmed, results := 0, 0
	loop := LocalLoop{Decisions: partialResultEvaluator{action: rules.Action{Kind: "play"}}, Journal: j, DecisionBudget: time.Second, ConfirmationBudget: 40 * time.Millisecond,
		Observe: func(ctx context.Context) (SemanticObservation, error) {
			if first {
				first = false
				return SemanticObservation{Source: "turn", MatchID: "m", Phase: "turn", CapturedAt: time.Now(), Eligible: true}, nil
			}
			select {
			case observation := <-resultFrame:
				return observation, nil
			case <-ctx.Done():
				return SemanticObservation{}, ctx.Err()
			}
		},
		Execute: func(_ context.Context, _ Snapshot, _ rules.Action, _ time.Time) error {
			err := j.ExecuteStep(Action{Kind: "click", Reference: "turn", X: 1, Y: 1}, func(a Action) error {
				return a.onReceipt(InputReceipt{Outcome: "delivered", EventsSent: 2, DeliveredAt: time.Now()})
			})
			now := time.Now()
			resultFrame <- SemanticObservation{Source: "result", MatchID: "m", Phase: "result", CapturedAt: now, CalibrationID: "cal", Frames: []matchlog.FrameEvidence{{Source: "prior", MatchID: "m", CapturedAt: now.Add(-time.Millisecond)}, {Source: "result", MatchID: "m", CapturedAt: now}}, Result: true, PartialResult: &stateestimate.OutcomeFacts{Finished: true, Winner: 1}}
			return err
		}, Confirm: func(rules.Action, SemanticObservation) bool { confirmed++; return true }, OnResult: func(SemanticObservation) { results++ }}
	err := loop.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "confirmation timed out") || confirmed != 0 || results != 0 || j.Pending() == nil {
		t.Fatalf("err=%v confirms=%d results=%d pending=%+v", err, confirmed, results, j.Pending())
	}
	for _, event := range sink.events {
		if event.EventType == "result_observed" || event.EventType == "action_confirmed" {
			t.Fatalf("unexpected event %+v", event)
		}
	}
}

func TestPartialResultConfirmsPassAfterDelivery(t *testing.T) {
	for _, tc := range []struct {
		name           string
		beforeDelivery bool
	}{{"fresh", false}, {"first frame before delivery", true}} {
		t.Run(tc.name, func(t *testing.T) {
			sink := &receiptSink{}
			j := NewActionJournal(sink)
			frames := make(chan SemanticObservation, 1)
			first := true
			adapter := nativeLoopAdapter{before: SemanticObservation{View: rules.View{Round: 3}}}
			results := 0
			loop := LocalLoop{Decisions: planEvaluator{}, Journal: j, DecisionBudget: time.Second, ConfirmationBudget: 50 * time.Millisecond,
				Observe: func(ctx context.Context) (SemanticObservation, error) {
					if first {
						first = false
						return SemanticObservation{Source: "turn", MatchID: "m", Phase: "turn", CapturedAt: time.Now(), Eligible: true}, nil
					}
					select {
					case o := <-frames:
						return o, nil
					case <-ctx.Done():
						return SemanticObservation{}, ctx.Err()
					}
				},
				Execute: func(_ context.Context, _ Snapshot, _ rules.Action, _ time.Time) error {
					at := time.Now()
					err := j.ExecuteStep(Action{Kind: "click", Reference: "turn", X: 1, Y: 1}, func(a Action) error {
						return a.onReceipt(InputReceipt{Outcome: "delivered", EventsSent: 2, DeliveredAt: at})
					})
					firstAt := at.Add(time.Nanosecond)
					if tc.beforeDelivery {
						firstAt = at.Add(-time.Nanosecond)
					}
					secondAt := at.Add(2 * time.Nanosecond)
					frames <- SemanticObservation{Source: "result", MatchID: "m", Phase: "result", CapturedAt: secondAt, CalibrationID: "cal", Frames: []matchlog.FrameEvidence{{Source: "prior", MatchID: "m", CapturedAt: firstAt}, {Source: "result", MatchID: "m", CapturedAt: secondAt}}, Result: true, PartialResult: &stateestimate.OutcomeFacts{Finished: true, Winner: 1}}
					return err
				}, Confirm: adapter.confirm, OnResult: func(SemanticObservation) { results++ }}
			err := loop.Run(context.Background())
			if tc.beforeDelivery {
				if err == nil || results != 0 || j.Pending() == nil {
					t.Fatalf("stale result: err=%v results=%d", err, results)
				}
			} else if err != nil || results != 1 || j.Pending() != nil || j.result == nil || *j.result != "loss" {
				t.Fatalf("pass result: err=%v results=%d pending=%+v result=%v", err, results, j.Pending(), j.result)
			}
		})
	}
}

func TestNativeSessionConfigTwoMatches(t *testing.T) {
	catalog, err := cards.Load("../../data/cards/14.10.0/en/cards.json")
	if err != nil {
		t.Fatal(err)
	}
	engine, err := rules.New(catalog)
	if err != nil {
		t.Fatal(err)
	}
	state, err := engine.Initial([2][]int{{132403}, {}}, [2][]int{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := state.View(0)
	model := uiphase.PhaseModel{Width: 32, Height: 16}
	images := map[string]image.Image{}
	stages := []string{"training", "mulligan", "confirm", "turn", "result", "deck-editor", "home"}
	for i, name := range stages {
		img := image.NewRGBA(image.Rect(0, 0, 32, 16))
		draw.Draw(img, img.Bounds(), image.NewUniform(color.RGBA{uint8(30 + i*40), 0, 0, 255}), image.Point{}, draw.Src)
		images[name] = img
		a := uiphase.Anchor{X: 0, Y: 0, Width: 16, Height: 8, MaxError: .01}
		a.Pixels = uiphase.AnchorPixels(img, a, 32, 16)
		model.Phases = append(model.Phases, uiphase.Phase{Name: name, Anchors: []uiphase.Anchor{a, a}})
	}
	phase, stream := "deck-editor", ""
	var recognizer *uiphase.Recognizer
	seq, inputs, matches := 0, 0, 0
	root := t.TempDir()
	session := TrainingSession{ID: "offline", LogRoot: root, StartMatch: 1, Matches: 2, TransitionBudget: time.Second,
		Bootstrap: []SessionTransition{{From: "deck-editor", To: "home", Input: Action{Kind: "click", X: 20, Y: 10}}, {From: "home", To: "training", Input: Action{Kind: "click", X: 21, Y: 10}}},
		Entry:     []SessionTransition{{From: "training", To: "mulligan", Input: Action{Kind: "click", X: 10, Y: 10}}, {From: "mulligan", To: "confirm", Input: Action{Kind: "click", X: 11, Y: 10}}, {From: "confirm", To: "turn", Input: Action{Kind: "click", X: 12, Y: 10}}},
		Exit:      []SessionTransition{{From: "result", To: "training", Input: Action{Kind: "click", X: 13, Y: 10}}},
		ObserveStage: func(_ context.Context, id string) (uiphase.Event, error) {
			if stream != id {
				var e error
				recognizer, e = uiphase.NewRecognizer(model, id, time.Second)
				if e != nil {
					return uiphase.Event{}, e
				}
				stream = id
			}
			seq++
			at := time.Now()
			return recognizer.Observe(uiphase.Frame{Ref: uiphase.FrameRef{ID: fmt.Sprint(seq), StreamID: id, CapturedAt: at, Width: 32, Height: 16}, Image: images[phase]}, time.Now())
		}, ExecuteInput: func(_ context.Context, a Action) error {
			inputs++
			if err := a.onReceipt(InputReceipt{Outcome: "delivered", EventsSent: 2, DeliveredAt: time.Now()}); err != nil {
				return err
			}
			switch a.ExpectedPhase {
			case "deck-editor":
				phase = "home"
			case "home":
				phase = "training"
			case "training":
				phase = "mulligan"
			case "mulligan":
				phase = "confirm"
			case "confirm":
				phase = "turn"
			case "result":
				phase = "training"
			default:
				t.Fatalf("unexpected stage %s", a.ExpectedPhase)
			}
			return nil
		}, RunMatch: func(ctx context.Context, id string, j *ActionJournal) (SemanticObservation, error) {
			matches++
			delivered := make(chan struct{})
			var result SemanticObservation
			loop := LocalLoop{Decisions: rules.NewEvaluator(engine), Journal: j, DecisionBudget: time.Second, ConfirmationBudget: time.Second,
				Observe: func(context.Context) (SemanticObservation, error) {
					select {
					case <-delivered:
						v := before
						v.Finished = true
						v.Winner = 1
						return withTestEvidence(SemanticObservation{Source: id + "/result", MatchID: id, Phase: "result", CapturedAt: time.Now(), View: v, Result: true}), nil
					default:
						return SemanticObservation{Source: id + "/turn", MatchID: id, Phase: "turn", CapturedAt: time.Now(), View: before, HandOrder: []string{state.Cards[0].ID}, Eligible: true}, nil
					}
				}, Execute: func(_ context.Context, _ Snapshot, a rules.Action, _ time.Time) error {
					if a.Kind != "play" {
						t.Errorf("action=%+v", a)
					}
					err := j.ExecuteStep(Action{Kind: "click", Reference: id + "/turn", X: 400, Y: 900}, func(input Action) error {
						return input.onReceipt(InputReceipt{Outcome: "delivered", EventsSent: 2, DeliveredAt: time.Now()})
					})
					phase = "result"
					close(delivered)
					return err
				}, Confirm: func(_ rules.Action, o SemanticObservation) bool { return o.Result }, OnResult: func(o SemanticObservation) { result = o }}
			err := loop.Run(ctx)
			return result, err
		}}
	profile := []byte(`{}`)
	profilePath := filepath.Join(root, "loop.json")
	if err = os.WriteFile(profilePath, profile, 0600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(profile)
	config := NativeSessionConfig{ID: session.ID, StateDir: root, LoopConfig: profilePath, LoopConfigSHA256: hex.EncodeToString(sum[:]), StartMatch: 1, Matches: 2, TransitionTimeoutMS: 1000, Entry: session.Entry, Exit: session.Exit, Bootstrap: session.Bootstrap}
	data, _ := json.Marshal(config)
	configPath := filepath.Join(root, "session.json")
	if err = os.WriteFile(configPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err = RunSessionConfig(context.Background(), configPath, func(c NativeSessionConfig) (SessionHost, error) {
		if c.LoopConfigSHA256 != config.LoopConfigSHA256 {
			t.Error("profile pin lost")
		}
		return offlineSessionHost{session}, nil
	}); err != nil {
		t.Fatal(err)
	}
	if matches != 2 || inputs != 10 || phase != "training" {
		t.Fatalf("matches=%d navigation=%d phase=%s", matches, inputs, phase)
	}
	for n := 1; n <= 2; n++ {
		matchName := fmt.Sprintf("%s-%06d", session.ID, n)
		events, err := matchlog.Read(filepath.Join(root, matchName, "events.jsonl"))
		if err != nil {
			t.Fatal(err)
		}
		if len(events) == 0 || events[0].EventType != "session_started" || events[0].SessionID != session.ID {
			t.Fatalf("missing session provenance: %+v", events)
		}
		samples, err := matchlog.ConfirmedTrainingEvents(events)
		if err != nil || len(samples) != 1 {
			t.Fatalf("match %d samples=%d err=%v", n, len(samples), err)
		}
		if events[len(events)-1].EventType != "match_completed" {
			t.Fatal("missing completed result")
		}
		training, err := os.ReadFile(filepath.Join(root, matchName, "training.jsonl"))
		if err != nil {
			t.Fatal(err)
		}
		var exported matchlog.TrainingSample
		if err = json.Unmarshal(training, &exported); err != nil || exported.Confirmation.EventType != "action_confirmed" || exported.Intent.ActionDomain != "gameplay" {
			t.Fatalf("export=%s err=%v", training, err)
		}
		if destination := os.Getenv("GWENT_OFFLINE_SESSION_ARTIFACTS"); destination != "" {
			matchDir := filepath.Join(destination, matchName)
			if err = os.MkdirAll(matchDir, 0700); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"events.jsonl", "training.jsonl", "COMPLETE"} {
				data, e := os.ReadFile(filepath.Join(root, matchName, name))
				if e != nil {
					t.Fatal(e)
				}
				if e = os.WriteFile(filepath.Join(matchDir, name), data, 0600); e != nil {
					t.Fatal(e)
				}
			}
		}
		var last uint64
		for _, e := range events {
			if e.MatchID != matchName {
				t.Fatalf("unsafe/mismatched match ID: %q", e.MatchID)
			}
			if e.Sequence != last+1 {
				t.Fatal("event sequence reset")
			}
			last = e.Sequence
		}
	}
}
