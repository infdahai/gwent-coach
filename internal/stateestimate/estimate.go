package stateestimate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"time"

	"gwentcoachmvp/internal/rules"
)

// Estimate is stateless and deterministic for the supplied clock. It never fills
// gaps from an earlier observation or a predicted action. Disagreement is
// unknown, whereas malformed capture metadata or inconsistent agreed facts are
// errors. Callers retain the current pending action when an observation is partial.
func Estimate(pair [2]Evidence, now time.Time, maxAge time.Duration) (Observation, error) {
	var out Observation
	if maxAge <= 0 || now.IsZero() {
		return out, fmt.Errorf("positive evidence age and explicit clock required")
	}
	a, b := pair[0], pair[1]
	for _, e := range pair {
		f := e.Frame
		age := now.Sub(f.CapturedAt)
		if f.ID == "" || f.MatchID == "" || f.CapturedAt.IsZero() || age < 0 || age > maxAge || f.Width <= 0 || f.Height <= 0 || f.Player < 0 || f.Player > 1 || e.CalibrationID == "" {
			return out, fmt.Errorf("missing, invalid or stale evidence")
		}
	}
	if a.Frame.ID == b.Frame.ID || !b.Frame.CapturedAt.After(a.Frame.CapturedAt) || a.Frame.MatchID != b.Frame.MatchID || a.Frame.Player != b.Frame.Player || a.Frame.Width != b.Frame.Width || a.Frame.Height != b.Frame.Height || a.CalibrationID != b.CalibrationID {
		return out, fmt.Errorf("distinct ordered captures in one match, layout and calibration required")
	}
	out.Frame, out.Sources, out.CalibrationID = b.Frame, [2]string{a.Frame.ID, b.Frame.ID}, b.CalibrationID
	out.Stage = "unknown"
	if a.Stage != "" && a.Stage == b.Stage {
		out.Stage = b.Stage
	}
	out.Facts = Facts{
		Round:   agree(a.Facts.Round, b.Facts.Round),
		Turn:    agree(a.Facts.Turn, b.Facts.Turn),
		Totals:  agree(a.Facts.Totals, b.Facts.Totals),
		Cards:   agreeCards(a.Facts.Cards, b.Facts.Cards),
		Outcome: agree(a.Facts.Outcome, b.Facts.Outcome),
	}
	provenance := FieldEvidence{Known: true, Frames: [2]FrameRef{a.Frame, b.Frame}, CalibrationID: b.CalibrationID}
	out.FieldEvidence = make(map[string]FieldEvidence)
	for _, f := range []struct {
		name  string
		known bool
	}{
		{"round", out.Facts.Round != nil}, {"turn", out.Facts.Turn != nil},
		{"totals", out.Facts.Totals != nil}, {"cards", out.Facts.Cards != nil}, {"outcome", out.Facts.Outcome != nil},
	} {
		if !f.known {
			out.Missing = append(out.Missing, f.name)
		} else {
			out.FieldEvidence[f.name] = provenance
		}
	}
	out.Targets = agreedTargets(a, b, provenance)
	if out.Facts.Cards != nil {
		for _, c := range out.Facts.Cards.Visible {
			if c.CardID == 0 {
				out.Missing = append(out.Missing, "card_identity")
				break
			}
		}
	}
	if len(out.Missing) == 0 {
		v, err := completeView(out.Facts, b.Frame.Player)
		if err != nil {
			return Observation{}, err
		}
		out.View = &v
		out.Eligible = out.Stage == "turn" && v.Turn == b.Frame.Player && !v.Passed[b.Frame.Player] && !v.BetweenRounds && !v.Finished
	}
	// Partial facts are part of the key too: becoming unknown must invalidate
	// an earlier complete observation, even if the UI stage stays the same.
	encoded, err := json.Marshal(struct {
		MatchID              string
		Player               int
		CalibrationID, Stage string
		Facts                Facts
		Eligible             bool
	}{b.Frame.MatchID, b.Frame.Player, out.CalibrationID, out.Stage, out.Facts, out.Eligible})
	if err != nil {
		return Observation{}, err
	}
	digest := sha256.Sum256(encoded)
	out.StateKey = hex.EncodeToString(digest[:])
	return out, nil
}

func agreedTargets(a, b Evidence, source FieldEvidence) []TargetObservation {
	valid := func(t Target, f FrameRef) bool {
		return (t.Kind == "control" || t.Kind == "hand-instance" || t.Kind == "row" || t.Kind == "board-target") &&
			t.ID != "" && t.LayoutID != "" && !t.Bounds.Empty() && t.Bounds.Min.X >= 0 && t.Bounds.Min.Y >= 0 && t.Bounds.Max.X <= f.Width && t.Bounds.Max.Y <= f.Height
	}
	var out []TargetObservation
	for _, target := range b.Targets {
		if !valid(target, b.Frame) {
			continue
		}
		countA, countB := 0, 0
		for _, t := range a.Targets {
			if t.Kind == target.Kind && t.ID == target.ID {
				countA++
				if t != target || !valid(t, a.Frame) {
					countA += 2
				}
			}
		}
		for _, t := range b.Targets {
			if t.Kind == target.Kind && t.ID == target.ID {
				countB++
			}
		}
		if countA == 1 && countB == 1 {
			out = append(out, TargetObservation{Target: target, Evidence: source})
		}
	}
	return out
}

func agree[T any](a, b *T) *T {
	if a == nil || b == nil || !reflect.DeepEqual(*a, *b) {
		return nil
	}
	v := *b
	return &v
}

func copyCards(c *CardsFacts) *CardsFacts {
	if c == nil {
		return nil
	}
	return &CardsFacts{Visible: append([]rules.Instance{}, c.Visible...), HandOrder: append([]string{}, c.HandOrder...)}
}

func agreeCards(a, b *CardsFacts) *CardsFacts {
	a, b = copyCards(a), copyCards(b)
	if a == nil || b == nil {
		return nil
	}
	// Enumeration order is not semantic; local hand order is semantic.
	sort.Slice(a.Visible, func(i, j int) bool { return a.Visible[i].ID < a.Visible[j].ID })
	sort.Slice(b.Visible, func(i, j int) bool { return b.Visible[i].ID < b.Visible[j].ID })
	return agree(a, b)
}

func completeView(f Facts, player int) (rules.View, error) {
	v := rules.View{Ruleset: "native-v1", Round: f.Round.Round, Wins: f.Round.Wins,
		Turn: f.Turn.Player, Passed: f.Turn.Passed, BetweenRounds: f.Turn.BetweenRounds,
		Score: f.Totals.Score, HandCount: f.Totals.HandCount, DeckCount: f.Totals.DeckCount,
		Finished: f.Outcome.Finished, Winner: f.Outcome.Winner,
		Visible: append([]rules.Instance{}, f.Cards.Visible...)}
	if v.Round < 1 || v.Round > 3 || v.Turn < 0 || v.Turn > 1 || v.Winner < -1 || v.Winner > 1 || (!v.Finished && v.Winner != -1) {
		return rules.View{}, fmt.Errorf("inconsistent round, turn or outcome evidence")
	}
	for p := 0; p < 2; p++ {
		if v.Wins[p] < 0 || v.Wins[p] > 2 || v.Score[p] < 0 || v.HandCount[p] < 0 || v.HandCount[p] > 10 || v.DeckCount[p] < 0 {
			return rules.View{}, fmt.Errorf("invalid public counts")
		}
	}
	ids, hand := map[string]bool{}, map[string]bool{}
	var score [2]int
	for _, c := range v.Visible {
		if c.ID == "" || ids[c.ID] || c.CardID <= 0 || c.Owner < 0 || c.Owner > 1 || c.Power < 0 || c.Power > 1000000 || c.Armor < 0 || c.Armor > 1000000 {
			return rules.View{}, fmt.Errorf("invalid visible card")
		}
		ids[c.ID] = true
		switch c.Zone {
		case rules.Hand:
			if c.Owner != player || c.Row != -1 {
				return rules.View{}, fmt.Errorf("hidden or misplaced hand card")
			}
			hand[c.ID] = true
		case rules.Board:
			if c.Row < 0 || c.Row > 1 {
				return rules.View{}, fmt.Errorf("invalid board row")
			}
			score[c.Owner] += c.Power
		case rules.Graveyard:
			if c.Row != -1 {
				return rules.View{}, fmt.Errorf("invalid graveyard row")
			}
		default:
			return rules.View{}, fmt.Errorf("hidden or unknown card zone")
		}
	}
	if score != v.Score || len(hand) != v.HandCount[player] || len(f.Cards.HandOrder) != len(hand) {
		return rules.View{}, fmt.Errorf("card evidence disagrees with scores or hand count")
	}
	for _, id := range f.Cards.HandOrder {
		if !hand[id] {
			return rules.View{}, fmt.Errorf("hand order incomplete or duplicated")
		}
		delete(hand, id)
	}
	return v, nil
}
