package observe

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"io"
	"strings"
	"testing"
	"time"

	"gwentcoachmvp/internal/cardmatch"
)

func matchTestConfig() ObservedMatchConfig {
	return ObservedMatchConfig{TimeoutMS: 2000, TransitionTimeoutMS: 500, UnknownTimeoutMS: 20,
		Start: image.Pt(790, 555), FinishMulligan: image.Pt(870, 1020), ConfirmMulligan: image.Pt(892, 635), Pass: image.Pt(1845, 540), EndTurn: image.Pt(1845, 540),
		Hand:  ObservedHandConfig{Clicks: []image.Point{image.Pt(500, 980), image.Pt(700, 980)}},
		Plays: []ObservedPlay{{ID: "defender", CardID: 142306, Row: image.Pt(900, 700)}}}
}

type matchTestHost struct {
	frames    []observedMatchFrame
	index     int
	sent      []observedMatchInput
	afterSend func(observedMatchInput) error
	mutate    func(*observedMatchFrame)
}

func (h *matchTestHost) host() observedMatchHost {
	return observedMatchHost{
		Observe: func(context.Context) (observedMatchFrame, error) {
			if h.index >= len(h.frames) {
				return observedMatchFrame{}, fmt.Errorf("fake host exhausted at frame %d", h.index)
			}
			f := h.frames[h.index]
			h.index++
			f.Reference, f.Captured, f.Width, f.Height = fmt.Sprintf("frame-%d", h.index), time.Now(), 1920, 1080
			if h.mutate != nil {
				h.mutate(&f)
			}
			return f, nil
		},
		Send: func(_ context.Context, _ observedMatchFrame, input observedMatchInput, receipt func(InputReceipt) error) error {
			h.sent = append(h.sent, input)
			if h.afterSend != nil {
				if err := h.afterSend(input); err != nil {
					return err
				}
			}
			return receipt(InputReceipt{Outcome: "delivered", EventsSent: 2, DeliveredAt: time.Now()})
		},
		Pause: func() error { return nil }, Wait: func(ctx context.Context) error { return ctx.Err() },
	}
}

func matchFrames(states ...string) []observedMatchFrame {
	var out []observedMatchFrame
	for _, state := range states {
		parts := strings.Split(state, "/")
		f := observedMatchFrame{navFrame: navFrame{Phase: parts[0]}, Plays: map[string]string{}}
		if len(parts) > 1 {
			f.Plays["defender"] = parts[1]
			if parts[1] == "ready" {
				f.Hand = []cardmatch.Result{{Status: "candidate", CardID: 142306, RunnerDistance: 20}, {Status: "unknown"}}
			}
		}
		out = append(out, f, f)
	}
	return out
}

func fullMatchFrames(auto bool) []observedMatchFrame {
	states := []string{"training", "mulligan", "mulligan-confirm", "turn/ready", "unknown/selected"}
	if !auto {
		states = append(states, "turn/placed")
	}
	states = append(states, "opponent/ended", "turn", "passed", "mulligan", "mulligan-confirm", "turn", "passed", "defeat")
	return matchFrames(states...)
}

func TestObservedMatchChoosesShuffledSafeSlot(t *testing.T) {
	for _, slot := range []int{0, 1} {
		t.Run(fmt.Sprint(slot), func(t *testing.T) {
			h := matchTestHost{frames: fullMatchFrames(true)}
			for _, i := range []int{6, 7} {
				h.frames[i].Hand = []cardmatch.Result{{Status: "unknown"}, {Status: "unknown"}}
				h.frames[i].Hand[slot] = cardmatch.Result{Status: "candidate", CardID: 142306, RunnerDistance: 20}
			}
			cfg := matchTestConfig()
			if err := runObservedMatch(context.Background(), cfg, h.host(), io.Discard); err != nil {
				t.Fatal(err)
			}
			selected := h.sent[3]
			if selected.Name != "select_card" || selected.Slot != slot || image.Pt(selected.Action.X, selected.Action.Y) != cfg.Hand.Clicks[slot] {
				t.Fatal(selected)
			}
		})
	}
}

func TestObservedMatchRejectsChangingHandIdentity(t *testing.T) {
	h := matchTestHost{frames: fullMatchFrames(true)}
	h.frames[7].Hand = []cardmatch.Result{{Status: "candidate", CardID: 142315, RunnerDistance: 20}, {Status: "unknown"}}
	if err := runObservedMatch(context.Background(), matchTestConfig(), h.host(), io.Discard); err == nil {
		t.Fatal("accepted changed hand")
	}
	for _, input := range h.sent {
		if input.Name == "select_card" {
			t.Fatal("selected inconsistent identity")
		}
	}
}

func TestObservedMatchWholeGame(t *testing.T) {
	for _, auto := range []bool{false, true} {
		t.Run(fmt.Sprintf("automatic-turn=%v", auto), func(t *testing.T) {
			h := matchTestHost{frames: fullMatchFrames(auto)}
			var log bytes.Buffer
			err := runObservedMatch(context.Background(), matchTestConfig(), h.host(), &log)
			if err != nil {
				t.Fatal(err)
			}
			var actions []string
			for _, a := range h.sent {
				actions = append(actions, a.Name)
			}
			want := "start_training,finish_mulligan,confirm_mulligan,select_card,place_card,"
			if !auto {
				want += "end_turn,"
			}
			want += "pass,finish_mulligan,confirm_mulligan,pass"
			if strings.Join(actions, ",") != want {
				t.Fatal(actions)
			}
			for _, status := range []string{"input_intent", "input_receipt", "delivered_unconfirmed", "card_play_confirmed", "result_confirmed", "match_completed"} {
				if !strings.Contains(log.String(), `"status":"`+status+`"`) {
					t.Fatalf("missing %s", status)
				}
			}
			if !strings.Contains(log.String(), `"round":2,"confirmed_plays":1`) {
				t.Fatal("missing qualified evidence")
			}
		})
	}
}

func TestObservedMatchResultRequiresPlay(t *testing.T) {
	h := matchTestHost{frames: matchFrames("training", "mulligan", "mulligan-confirm", "turn", "passed", "mulligan", "mulligan-confirm", "turn", "passed", "defeat")}
	var log bytes.Buffer
	err := runObservedMatch(context.Background(), matchTestConfig(), h.host(), &log)
	if err == nil || !strings.Contains(err.Error(), "lacks confirmed card") || strings.Contains(log.String(), `"status":"result_confirmed"`) {
		t.Fatalf("err=%v log=%s", err, &log)
	}
}

func TestObservedMatchRejectsResultAlone(t *testing.T) {
	h := matchTestHost{frames: matchFrames("defeat")}
	err := runObservedMatch(context.Background(), matchTestConfig(), h.host(), io.Discard)
	if err == nil || len(h.sent) != 0 {
		t.Fatalf("err=%v inputs=%v", err, h.sent)
	}
}

func TestObservedMatchNeverReplaysAmbiguousInput(t *testing.T) {
	h := matchTestHost{frames: fullMatchFrames(false), afterSend: func(input observedMatchInput) error {
		if input.Name == "place_card" {
			return errors.New("partial input")
		}
		return nil
	}}
	var log bytes.Buffer
	err := runObservedMatch(context.Background(), matchTestConfig(), h.host(), &log)
	if err == nil || !strings.Contains(err.Error(), "partial input") || len(h.sent) != 5 || strings.Contains(log.String(), `"status":"card_play_confirmed"`) {
		t.Fatalf("err=%v sends=%v", err, h.sent)
	}
}

func TestObservedMatchCancellation(t *testing.T) {
	for _, boundary := range []string{"before", "observe", "place_card", "wait"} {
		t.Run(boundary, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			h := matchTestHost{frames: fullMatchFrames(false)}
			h.afterSend = func(a observedMatchInput) error {
				if a.Name == boundary {
					cancel()
				}
				return nil
			}
			host := h.host()
			if boundary == "before" {
				cancel()
			}
			if boundary == "observe" {
				h.mutate = func(*observedMatchFrame) { cancel() }
			}
			if boundary == "wait" {
				host.Wait = func(context.Context) error { cancel(); return ctx.Err() }
			}
			err := runObservedMatch(ctx, matchTestConfig(), host, io.Discard)
			if !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			want := 0
			if boundary == "place_card" {
				want = 5
			}
			if len(h.sent) != want {
				t.Fatal(h.sent)
			}
		})
	}
}

func TestObservedMatchRejectsReusedEvidenceAndUnconfirmedPlacement(t *testing.T) {
	for _, mode := range []string{"same-reference", "same-time", "placement-skipped", "one-placement-frame", "one-round"} {
		t.Run(mode, func(t *testing.T) {
			h := matchTestHost{frames: fullMatchFrames(true)}
			switch mode {
			case "same-reference":
				h.mutate = func(f *observedMatchFrame) { f.Reference = "duplicate" }
			case "same-time":
				at := time.Now()
				h.mutate = func(f *observedMatchFrame) { f.Captured = at }
			case "placement-skipped":
				h.frames = append(matchFrames("training", "mulligan", "mulligan-confirm", "turn/ready", "unknown/selected"), matchFrames("turn", "passed", "defeat")...)
			case "one-placement-frame":
				h.frames = append(h.frames[:11], h.frames[12:]...)
			case "one-round":
				h.frames = append(matchFrames("training", "mulligan", "mulligan-confirm", "turn/ready", "unknown/selected", "opponent/ended", "turn", "passed"), matchFrames("defeat")...)
			}
			var log bytes.Buffer
			err := runObservedMatch(context.Background(), matchTestConfig(), h.host(), &log)
			if err == nil || strings.Contains(log.String(), `"status":"result_confirmed"`) {
				t.Fatalf("err=%v log=%s", err, &log)
			}
		})
	}
}

func TestObservedMatchOptionalExitAndStopLatch(t *testing.T) {
	cfg := matchTestConfig()
	exit := image.Pt(960, 1027)
	cfg.Exit = &exit
	h := matchTestHost{frames: append(fullMatchFrames(true), matchFrames("training")...)}
	if err := runObservedMatch(context.Background(), cfg, h.host(), io.Discard); err != nil {
		t.Fatal(err)
	}
	if h.sent[len(h.sent)-1].Name != "exit_result" {
		t.Fatal(h.sent)
	}
	host := h.host()
	host.Pause = func() error { return errors.New("STOP") }
	err := runObservedMatch(context.Background(), cfg, host, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "STOP") {
		t.Fatal(err)
	}
}

func TestObservedMatchTimeoutsAndUnknownScreens(t *testing.T) {
	for _, mode := range []string{"blocking-capture", "unknown", "unexpected"} {
		t.Run(mode, func(t *testing.T) {
			cfg := matchTestConfig()
			cfg.TransitionTimeoutMS = 50
			cfg.UnknownTimeoutMS = 1
			h := matchTestHost{}
			host := h.host()
			host.Observe = func(ctx context.Context) (observedMatchFrame, error) {
				if mode == "blocking-capture" {
					<-ctx.Done()
					return observedMatchFrame{}, ctx.Err()
				}
				h.index++
				phase := "unknown"
				if mode == "unexpected" {
					phase = "target-menu"
				}
				return observedMatchFrame{navFrame: navFrame{Phase: phase, Reference: fmt.Sprint(h.index), Captured: time.Now(), Width: 1920, Height: 1080}}, nil
			}
			err := runObservedMatch(context.Background(), cfg, host, io.Discard)
			if err == nil || len(h.sent) > 0 {
				t.Fatalf("err=%v sends=%v", err, h.sent)
			}
			if mode == "blocking-capture" && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatal(err)
			}
			if mode == "unknown" && !strings.Contains(err.Error(), "unknown screen") {
				t.Fatal(err)
			}
		})
	}
}

type matchFailWriter struct{ status string }

func (w matchFailWriter) Write(p []byte) (int, error) {
	if bytes.Contains(p, []byte(`"status":"`+w.status+`"`)) {
		return 0, errors.New("journal unavailable")
	}
	return len(p), nil
}

func TestObservedMatchJournalFailureStopsInput(t *testing.T) {
	for _, status := range []string{"input_intent", "input_receipt", "delivered_unconfirmed"} {
		t.Run(status, func(t *testing.T) {
			h := matchTestHost{frames: fullMatchFrames(false)}
			err := runObservedMatch(context.Background(), matchTestConfig(), h.host(), matchFailWriter{status: status})
			want := 1
			if status == "input_intent" {
				want = 0
			}
			if err == nil || len(h.sent) != want {
				t.Fatalf("err=%v sends=%v", err, h.sent)
			}
		})
	}
}

func TestObservedMatchRefusesMissingReceipt(t *testing.T) {
	h := matchTestHost{frames: fullMatchFrames(false)}
	host := h.host()
	host.Send = func(context.Context, observedMatchFrame, observedMatchInput, func(InputReceipt) error) error {
		return nil
	}
	if err := runObservedMatch(context.Background(), matchTestConfig(), host, io.Discard); err == nil || !strings.Contains(err.Error(), "delivery unresolved") {
		t.Fatal(err)
	}
}

func TestNamedCardSkipsQuotedReferences(t *testing.T) {
	names := []ObservedCardName{{Name: "艾斯卡尔", CardID: 112202}, {Name: "维瑟米尔", CardID: 112203}}
	cases := []struct {
		text string
		want int
	}{
		{"卡 牌 大 师 维 瑟 米 尔 猎 魔 人 部 署 ： 从 牌 组 中 召 唤 “ 艾 斯 卡 尔 ”", 112203},
		{"卡 牌 大 师 艾 斯 卡 尔 猎 魔 人 部 署 ： 从 牌 组 中 召 唤 “ 维 瑟 米 尔 ”", 112202},
		{"卡 牌 大 师 9 / 10 放 弃 跟 牌", 0},
	}
	for _, c := range cases {
		if got := namedCard(names, c.text); got != c.want {
			t.Fatalf("namedCard(%q)=%d want %d", c.text, got, c.want)
		}
	}
}

func TestObservedMatchIdentifiesHandByName(t *testing.T) {
	states := []string{"training", "mulligan", "mulligan-confirm", "turn/ready", "turn/ready", "unknown/selected", "turn/placed", "opponent/ended", "turn", "passed", "mulligan", "mulligan-confirm", "turn", "passed", "defeat"}
	h := matchTestHost{frames: matchFrames(states...)}
	cfg := matchTestConfig()
	cfg.Hand.Names = []ObservedCardName{{Name: "玛哈坎勇士", CardID: 200042}, {Name: "狼群", CardID: 132403}}
	cfg.Plays[0].CardID = 200042
	var hovered []int
	host := h.host()
	host.Identify = func(_ context.Context, _ observedMatchFrame, slot int) (int, observedMatchFrame, error) {
		hovered = append(hovered, slot)
		f := observedMatchFrame{navFrame: navFrame{Reference: fmt.Sprintf("hover-%d-%d", slot, len(hovered)), Captured: time.Now(), Phase: "turn"}, Plays: map[string]string{"defender": "ready"}}
		if slot == 1 {
			return 200042, f, nil
		}
		return 132403, f, nil
	}
	if err := runObservedMatch(context.Background(), cfg, host, io.Discard); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(hovered) != "[0 1]" {
		t.Fatalf("hovered slots %v", hovered)
	}
	var selected *observedMatchInput
	for i := range h.sent {
		if h.sent[i].Name == "select_card" {
			selected = &h.sent[i]
		}
	}
	if selected == nil || selected.Slot != 1 || !selected.Identified || selected.PlayID != "defender" {
		t.Fatalf("select %+v", selected)
	}
	if selected.Action.X != cfg.Hand.Clicks[1].X || selected.Action.Y != cfg.Hand.Clicks[1].Y {
		t.Fatalf("select point %+v", selected.Action)
	}
}

func TestObservedMatchRequiresIdentificationHost(t *testing.T) {
	h := matchTestHost{frames: matchFrames("training", "mulligan", "mulligan-confirm", "turn/ready")}
	cfg := matchTestConfig()
	cfg.Hand.Names = []ObservedCardName{{Name: "玛哈坎勇士", CardID: 200042}, {Name: "狼群", CardID: 132403}}
	cfg.Plays[0].CardID = 200042
	if err := runObservedMatch(context.Background(), cfg, h.host(), io.Discard); err == nil || !strings.Contains(err.Error(), "unavailable") {
		t.Fatal(err)
	}
}

// passBoundaryFrames builds a two-round match whose pass transitions use the
// supplied successor states. Each round plays once (auto-ended), then passes.
func passBoundaryFrames(round1Successor, round2Successor []string) []observedMatchFrame {
	states := []string{"training", "mulligan", "mulligan-confirm", "turn/ready", "unknown/selected", "opponent/ended", "turn"}
	states = append(states, round1Successor...)
	states = append(states, "mulligan-confirm", "turn")
	states = append(states, round2Successor...)
	return matchFrames(states...)
}

// TestObservedMatchPassFastTransitions proves a pass that is followed directly
// by the next mulligan or a result is confirmed from the same fresh pair. The
// old intermediate stages must keep working, no input may be replayed, and the
// fake host must not be over-read (it fails when exhausted).
func TestObservedMatchPassFastTransitions(t *testing.T) {
	cases := []struct {
		name           string
		round1, round2 []string
	}{
		{name: "direct-mulligan-and-result", round1: []string{"mulligan"}, round2: []string{"defeat"}},
		{name: "passed-intermediate", round1: []string{"passed", "mulligan"}, round2: []string{"passed", "defeat"}},
		{name: "opponent-intermediate", round1: []string{"opponent", "mulligan"}, round2: []string{"opponent", "defeat"}},
		{name: "mixed-direct-and-passed", round1: []string{"mulligan"}, round2: []string{"passed", "defeat"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := matchTestHost{frames: passBoundaryFrames(tc.round1, tc.round2)}
			var log bytes.Buffer
			if err := runObservedMatch(context.Background(), matchTestConfig(), h.host(), &log); err != nil {
				t.Fatal(err)
			}
			var actions []string
			for _, input := range h.sent {
				actions = append(actions, input.Name)
			}
			want := "start_training,finish_mulligan,confirm_mulligan,select_card,place_card,pass,finish_mulligan,confirm_mulligan,pass"
			if strings.Join(actions, ",") != want {
				t.Fatalf("actions=%v", actions)
			}
			if !strings.Contains(log.String(), `"status":"result_confirmed"`) {
				t.Fatal("missing result_confirmed")
			}
			if !strings.Contains(log.String(), `"round":2,"confirmed_plays":1`) {
				t.Fatal("missing qualified round-2 evidence")
			}
		})
	}
}

// TestObservedMatchDirectResultStillRequiresRoundGate proves a direct result
// after a single round is observed but never fabricated into a qualified one.
func TestObservedMatchDirectResultStillRequiresRoundGate(t *testing.T) {
	states := []string{"training", "mulligan", "mulligan-confirm", "turn/ready", "unknown/selected", "opponent/ended", "turn", "defeat"}
	h := matchTestHost{frames: matchFrames(states...)}
	var log bytes.Buffer
	err := runObservedMatch(context.Background(), matchTestConfig(), h.host(), &log)
	if err == nil || !strings.Contains(err.Error(), "lacks confirmed card") || strings.Contains(log.String(), `"status":"result_confirmed"`) {
		t.Fatalf("err=%v log=%s", err, &log)
	}
	if !strings.Contains(log.String(), `"status":"pass_confirmed"`) {
		t.Fatal("direct result should still confirm the pass")
	}
}

// TestObservedMatchPassOnlyStillUnqualified proves a direct mulligan/result
// observed after a pass cannot qualify a match with no confirmed card play.
func TestObservedMatchPassOnlyStillUnqualified(t *testing.T) {
	states := []string{"training", "mulligan", "mulligan-confirm", "turn", "mulligan", "mulligan-confirm", "turn", "defeat"}
	h := matchTestHost{frames: matchFrames(states...)}
	var log bytes.Buffer
	err := runObservedMatch(context.Background(), matchTestConfig(), h.host(), &log)
	if err == nil || !strings.Contains(err.Error(), "lacks confirmed card") || strings.Contains(log.String(), `"status":"result_confirmed"`) {
		t.Fatalf("err=%v log=%s", err, &log)
	}
}
