package stateestimate

import (
	"testing"
	"time"

	"gwentcoachmvp/internal/rules"
)

func completePair() ([2]Evidence, time.Time) {
	now := time.Date(2026, 11, 1, 0, 0, 1, 0, time.UTC)
	facts := Facts{Round: &RoundFacts{Round: 1}, Turn: &TurnFacts{Player: 0}, Totals: &TotalsFacts{HandCount: [2]int{1, 1}, DeckCount: [2]int{15, 15}},
		Cards: &CardsFacts{Visible: []rules.Instance{{ID: "own", CardID: 152405, Owner: 0, Power: 3, Zone: rules.Hand, Row: -1}}, HandOrder: []string{"own"}}, Outcome: &OutcomeFacts{Winner: -1}}
	a := Evidence{Frame: FrameRef{ID: "a", MatchID: "m", CapturedAt: now.Add(-time.Second), Width: 1920, Height: 1080, Player: 0}, CalibrationID: "cal", Stage: "turn", Facts: facts}
	b := a
	b.Frame.ID = "b"
	b.Frame.CapturedAt = now.Add(-500 * time.Millisecond)
	return [2]Evidence{a, b}, now
}

func TestSameStageDifferentBoardChangesObservation(t *testing.T) {
	pair, now := completePair()
	before, err := Estimate(pair, now, 2*time.Second)
	if err != nil || !before.Eligible || before.View == nil {
		t.Fatalf("before: %+v %v", before, err)
	}
	pair[0].Facts.Cards.Visible = append(pair[0].Facts.Cards.Visible, rules.Instance{ID: "enemy", CardID: 152405, Owner: 1, Power: 5, Zone: rules.Board, Row: 0})
	pair[0].Facts.Totals.Score[1] = 5
	after, err := Estimate(pair, now, 2*time.Second)
	if err != nil || after.View == nil || !after.Eligible || after.View.Score[1] != 5 || before.StateKey == after.StateKey || before.Stage != after.Stage {
		t.Fatalf("after: %+v %v", after, err)
	}
	if before.View.Score[1] != 0 || len(before.View.Visible) != 1 {
		t.Fatal("returned observation aliases evidence")
	}
}

func TestUnknownDoesNotReusePreviousState(t *testing.T) {
	pair, now := completePair()
	known, _ := Estimate(pair, now, 2*time.Second)
	pair[1].Facts.Totals = nil
	partial, err := Estimate(pair, now, 2*time.Second)
	if err != nil || partial.View != nil || partial.Eligible || partial.Facts.Totals != nil || partial.StateKey == known.StateKey {
		t.Fatalf("unknown promoted: %+v %v", partial, err)
	}
	pair[0].Facts = Facts{}
	pair[1].Facts = Facts{}
	stageOnly, err := Estimate(pair, now, 2*time.Second)
	if err != nil || stageOnly.Stage != "turn" || stageOnly.View != nil || stageOnly.Eligible {
		t.Fatalf("stage supplied state: %+v %v", stageOnly, err)
	}
}

func TestConflictingEvidenceStaysUnknown(t *testing.T) {
	pair, now := completePair()
	pair[1].Facts.Turn = &TurnFacts{Player: 1}
	o, err := Estimate(pair, now, 2*time.Second)
	if err != nil || o.Facts.Turn != nil || o.View != nil || o.Eligible {
		t.Fatalf("conflict promoted: %+v %v", o, err)
	}
	pair, now = completePair()
	pair[1].Stage = "unknown"
	o, err = Estimate(pair, now, 2*time.Second)
	if err != nil || o.View == nil || o.Stage != "unknown" || o.Eligible {
		t.Fatalf("unknown stage promoted: %+v %v", o, err)
	}
}

func TestEvidenceBindingAndFreshness(t *testing.T) {
	cases := map[string]func(*[2]Evidence){
		"same capture":          func(p *[2]Evidence) { p[1].Frame.ID = p[0].Frame.ID },
		"same time":             func(p *[2]Evidence) { p[1].Frame.CapturedAt = p[0].Frame.CapturedAt },
		"different match":       func(p *[2]Evidence) { p[1].Frame.MatchID = "other" },
		"different player":      func(p *[2]Evidence) { p[1].Frame.Player = 1 },
		"different geometry":    func(p *[2]Evidence) { p[1].Frame.Width = 960 },
		"different calibration": func(p *[2]Evidence) { p[1].CalibrationID = "other" },
		"stale first":           func(p *[2]Evidence) { p[0].Frame.CapturedAt = p[0].Frame.CapturedAt.Add(-time.Hour) },
		"future second":         func(p *[2]Evidence) { p[1].Frame.CapturedAt = p[1].Frame.CapturedAt.Add(time.Hour) },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			pair, now := completePair()
			change(&pair)
			if _, err := Estimate(pair, now, 2*time.Second); err == nil {
				t.Fatal("accepted invalid pair")
			}
		})
	}
}

func TestStableKeyAndHandOrder(t *testing.T) {
	pair, now := completePair()
	pair[0].Facts.Cards.Visible = append(pair[0].Facts.Cards.Visible, rules.Instance{ID: "own2", CardID: 152405, Owner: 0, Power: 3, Zone: rules.Hand, Row: -1})
	pair[0].Facts.Cards.HandOrder = append(pair[0].Facts.Cards.HandOrder, "own2")
	pair[0].Facts.Totals.HandCount[0] = 2
	a, err := Estimate(pair, now, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	pair[0].Frame.ID = "c"
	pair[1].Frame.ID = "d"
	pair[0].Frame.CapturedAt = pair[0].Frame.CapturedAt.Add(50 * time.Millisecond)
	pair[1].Frame.CapturedAt = pair[1].Frame.CapturedAt.Add(50 * time.Millisecond)
	pair[0].Facts.Cards.Visible[0], pair[0].Facts.Cards.Visible[1] = pair[0].Facts.Cards.Visible[1], pair[0].Facts.Cards.Visible[0]
	b, err := Estimate(pair, now, 2*time.Second)
	if err != nil || a.StateKey != b.StateKey {
		t.Fatalf("equivalent evidence changed key: %v", err)
	}
	pair[0].Facts.Cards.HandOrder = []string{"own2", "own"}
	c, err := Estimate(pair, now, 2*time.Second)
	if err != nil || c.StateKey == b.StateKey {
		t.Fatalf("hand order change lost: %v", err)
	}
}

func TestKnownEmptyHandDiffersFromUnknown(t *testing.T) {
	pair, now := completePair()
	pair[0].Facts.Cards = &CardsFacts{}
	pair[1].Facts.Cards = &CardsFacts{}
	pair[0].Facts.Totals.HandCount[0] = 0
	known, err := Estimate(pair, now, 2*time.Second)
	if err != nil || known.View == nil || !known.Eligible || known.View.HandCount[0] != 0 {
		t.Fatalf("known empty state lost: %+v %v", known, err)
	}
	pair[1].Facts.Cards = nil
	unknown, err := Estimate(pair, now, 2*time.Second)
	if err != nil || unknown.View != nil || unknown.Eligible || unknown.StateKey == known.StateKey {
		t.Fatalf("unknown treated as empty: %+v %v", unknown, err)
	}
}

func TestKnownNonActionableStatesRemainIneligible(t *testing.T) {
	for name, change := range map[string]func(Facts){
		"opponent turn":  func(f Facts) { f.Turn.Player = 1 },
		"passed":         func(f Facts) { f.Turn.Passed[0] = true },
		"between rounds": func(f Facts) { f.Turn.BetweenRounds = true },
		"finished":       func(f Facts) { f.Outcome.Finished = true },
	} {
		t.Run(name, func(t *testing.T) {
			pair, now := completePair()
			change(pair[0].Facts)
			o, err := Estimate(pair, now, 2*time.Second)
			if err != nil || o.View == nil || o.Eligible {
				t.Fatalf("non-actionable state: %+v %v", o, err)
			}
		})
	}
}

func TestUnknownIdentityAndInconsistentScores(t *testing.T) {
	pair, now := completePair()
	pair[0].Facts.Cards.Visible[0].CardID = 0
	o, err := Estimate(pair, now, 2*time.Second)
	if err != nil || o.View != nil || o.Eligible {
		t.Fatalf("unknown identity promoted: %v", err)
	}
	pair, now = completePair()
	pair[0].Facts.Totals.Score[0] = 10
	if _, err := Estimate(pair, now, 2*time.Second); err == nil {
		t.Fatal("accepted score without board evidence")
	}
}
