package observe

import (
	"context"
	"errors"
	"fmt"
	"image"
	"strings"
	"testing"
	"time"

	"gwentcoachmvp/internal/cardmatch"
)

// playTxnFrame builds one observed frame for the play-transaction tests.
func playTxnFrame(phase, playState string, ref string) observedMatchFrame {
	hand := []cardmatch.Result{}
	if playState == "ready" {
		hand = []cardmatch.Result{
			{Status: "candidate", CardID: 142306, RunnerDistance: 20},
			{Status: "unknown"},
		}
	}
	return observedMatchFrame{
		navFrame: navFrame{Phase: phase, Reference: ref, Captured: time.Now(), Width: 1920, Height: 1080},
		Plays:    map[string]string{"defender": playState},
		Hand:     hand,
	}
}

func playTxnConfig() ObservedMatchConfig {
	return ObservedMatchConfig{
		TimeoutMS: 2000, TransitionTimeoutMS: 500, UnknownTimeoutMS: 20,
		Start: image.Pt(790, 555), FinishMulligan: image.Pt(870, 1020), ConfirmMulligan: image.Pt(892, 635),
		Pass: image.Pt(1845, 540), EndTurn: image.Pt(1845, 540),
		Hand:  ObservedHandConfig{Clicks: []image.Point{image.Pt(500, 980), image.Pt(700, 980)}},
		Plays: []ObservedPlay{{ID: "defender", CardID: 142306, Row: image.Pt(900, 700)}},
	}
}

// playTxnChannel builds the shared channel over a fixed frame feed. It keeps
// the two-consecutive-frame rule from the real await but omits capture-age
// checks so static test frames remain usable.
func playTxnChannel(ctx context.Context, frames []observedMatchFrame) (observedPlayChannel, *[]observedMatchInput) {
	index := 0
	var sent []observedMatchInput
	await := func(playID string, expected, waiting []string) ([2]observedMatchFrame, error) {
		var previous observedMatchFrame
		for index < len(frames) {
			if err := ctx.Err(); err != nil {
				return [2]observedMatchFrame{}, err
			}
			frame := frames[index]
			index++
			phase, prior := frame.Phase, previous.Phase
			if playID != "" {
				phase, prior = frame.Plays[playID], previous.Plays[playID]
			}
			switch {
			case phaseIn(phase, expected):
				if previous.Reference != "" && prior == phase {
					return [2]observedMatchFrame{previous, frame}, nil
				}
				previous = frame
			case phaseIn(phase, waiting):
				previous = observedMatchFrame{}
			default:
				return [2]observedMatchFrame{}, fmt.Errorf("unexpected phase %q while awaiting %v", phase, expected)
			}
		}
		return [2]observedMatchFrame{}, fmt.Errorf("frame feed exhausted awaiting %v", expected)
	}
	send := func(pair [2]observedMatchFrame, input observedMatchInput) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		sent = append(sent, input)
		return nil
	}
	return observedPlayChannel{
		await:   await,
		send:    send,
		confirm: func(string, [2]observedMatchFrame) error { return nil },
	}, &sent
}

func readyPair() [2]observedMatchFrame {
	return [2]observedMatchFrame{
		playTxnFrame("turn", "ready", "seed-0"),
		playTxnFrame("turn", "ready", "seed-1"),
	}
}

// TestPlaySelectedCardAutoEnded verifies an automatically ended turn sends no
// extra EndTurn and counts one placed card.
func TestPlaySelectedCardAutoEnded(t *testing.T) {
	frames := []observedMatchFrame{
		playTxnFrame("turn", "selected", "f0"),
		playTxnFrame("turn", "selected", "f1"),
		playTxnFrame("turn", "ended", "f2"),
		playTxnFrame("turn", "ended", "f3"),
	}
	ch, sent := playTxnChannel(context.Background(), frames)
	plays := 0

	pair, err := playSelectedCard(ch, playTxnConfig(), readyPair(), playTxnConfig().Plays[0], 0, false, &plays)
	if err != nil {
		t.Fatal(err)
	}
	if plays != 1 {
		t.Fatalf("plays=%d, want 1", plays)
	}
	if got := inputNames(*sent); strings.Join(got, ",") != "select_card,place_card" {
		t.Fatalf("sent %v, want select_card,place_card", got)
	}
	if pair[1].Plays["defender"] != "ended" {
		t.Fatalf("final play phase=%q, want ended", pair[1].Plays["defender"])
	}
}

// TestPlaySelectedCardExplicitEndTurn verifies a placed-but-not-auto-ended
// card sends exactly one EndTurn.
func TestPlaySelectedCardExplicitEndTurn(t *testing.T) {
	frames := []observedMatchFrame{
		playTxnFrame("turn", "selected", "f0"),
		playTxnFrame("turn", "selected", "f1"),
		playTxnFrame("turn", "placed", "f2"),
		playTxnFrame("turn", "placed", "f3"),
		playTxnFrame("turn", "ended", "f4"),
		playTxnFrame("turn", "ended", "f5"),
	}
	ch, sent := playTxnChannel(context.Background(), frames)
	plays := 0

	pair, err := playSelectedCard(ch, playTxnConfig(), readyPair(), playTxnConfig().Plays[0], 0, false, &plays)
	if err != nil {
		t.Fatal(err)
	}
	if plays != 1 {
		t.Fatalf("plays=%d, want 1", plays)
	}
	if got := inputNames(*sent); strings.Join(got, ",") != "select_card,place_card,end_turn" {
		t.Fatalf("sent %v, want select_card,place_card,end_turn", got)
	}
	if pair[1].Plays["defender"] != "ended" {
		t.Fatalf("final play phase=%q, want ended", pair[1].Plays["defender"])
	}
}

// TestPlaySelectedCardCountsOnlyVerifiedPlacement verifies that a placement
// which never yields two fresh frames does not increment the play count.
func TestPlaySelectedCardCountsOnlyVerifiedPlacement(t *testing.T) {
	// Only the select transition is observed; the placement never settles.
	frames := []observedMatchFrame{
		playTxnFrame("turn", "selected", "f0"),
		playTxnFrame("turn", "selected", "f1"),
	}
	ch, sent := playTxnChannel(context.Background(), frames)
	plays := 0

	if _, err := playSelectedCard(ch, playTxnConfig(), readyPair(), playTxnConfig().Plays[0], 0, false, &plays); err == nil {
		t.Fatal("accepted unverified placement")
	}
	if plays != 0 {
		t.Fatalf("plays=%d, want 0 before placement is verified", plays)
	}
	if got := inputNames(*sent); strings.Join(got, ",") != "select_card,place_card" {
		t.Fatalf("sent %v", got)
	}
}

// TestPlaySelectedCardReportsSlotAndIdentity verifies the select input carries
// the chosen hand slot, name-identification flag and calibrated click point.
func TestPlaySelectedCardReportsSlotAndIdentity(t *testing.T) {
	frames := []observedMatchFrame{
		playTxnFrame("turn", "selected", "f0"),
		playTxnFrame("turn", "selected", "f1"),
		playTxnFrame("turn", "ended", "f2"),
		playTxnFrame("turn", "ended", "f3"),
	}
	ch, sent := playTxnChannel(context.Background(), frames)
	plays := 0
	cfg := playTxnConfig()

	if _, err := playSelectedCard(ch, cfg, readyPair(), cfg.Plays[0], 1, true, &plays); err != nil {
		t.Fatal(err)
	}
	selectInput := (*sent)[0]
	if selectInput.Name != "select_card" || selectInput.PlayID != "defender" || selectInput.Slot != 1 || !selectInput.Identified {
		t.Fatalf("select input %+v", selectInput)
	}
	if selectInput.Action.X != cfg.Hand.Clicks[1].X || selectInput.Action.Y != cfg.Hand.Clicks[1].Y {
		t.Fatalf("select point %+v", selectInput.Action)
	}
}

// TestPlaySelectedCardNeverResendsAfterFailure verifies an ambiguous input is
// not replayed: the transaction returns as soon as a send fails.
func TestPlaySelectedCardNeverResendsAfterFailure(t *testing.T) {
	frames := []observedMatchFrame{
		playTxnFrame("turn", "selected", "f0"),
		playTxnFrame("turn", "selected", "f1"),
	}
	ch, sent := playTxnChannel(context.Background(), frames)
	abort := errors.New("partial input")
	ch.send = func(_ [2]observedMatchFrame, input observedMatchInput) error {
		*sent = append(*sent, input)
		if input.Name == "place_card" {
			return abort
		}
		return nil
	}
	plays := 0
	_, err := playSelectedCard(ch, playTxnConfig(), readyPair(), playTxnConfig().Plays[0], 0, false, &plays)
	if !errors.Is(err, abort) {
		t.Fatalf("err=%v, want %v", err, abort)
	}
	if got := inputNames(*sent); strings.Join(got, ",") != "select_card,place_card" {
		t.Fatalf("replayed after failure: %v", got)
	}
	if plays != 0 {
		t.Fatalf("plays=%d, want 0", plays)
	}
}

// TestPlaySelectedCardCancellation verifies cancellation stops before input.
func TestPlaySelectedCardCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ch, sent := playTxnChannel(ctx, nil)
	plays := 0
	_, err := playSelectedCard(ch, playTxnConfig(), readyPair(), playTxnConfig().Plays[0], 0, false, &plays)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v, want context.Canceled", err)
	}
	if len(*sent) != 0 {
		t.Fatalf("sent %v after cancellation", inputNames(*sent))
	}
}

// TestContainsAny covers the shared phase helper.
func TestContainsAny(t *testing.T) {
	if !phaseIn("mulligan", []string{"mulligan", "victory"}) {
		t.Fatal("missed mulligan")
	}
	if phaseIn("passed", []string{"mulligan", "victory"}) {
		t.Fatal("matched passed")
	}
	if phaseIn("", []string{"mulligan"}) {
		t.Fatal("matched empty phase")
	}
}

func inputNames(inputs []observedMatchInput) []string {
	names := make([]string, len(inputs))
	for i, input := range inputs {
		names[i] = input.Name
	}
	return names
}

func phaseIn(phase string, set []string) bool {
	if phase == "" {
		return false
	}
	for _, candidate := range set {
		if candidate == phase {
			return true
		}
	}
	return false
}
