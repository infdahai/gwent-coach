package rules

import (
	"fmt"
	"reflect"
)

// View deliberately contains no opposing hand identities or either deck order.
// Hidden counts remain visible. Unknown opponent actions require reconciliation.
type View struct {
	Ruleset                     string
	Round, Turn, Revision       int
	Wins                        [2]int
	Passed                      [2]bool
	Score, HandCount, DeckCount [2]int
	BetweenRounds, Finished     bool
	Winner                      int
	Visible                     []Instance
	StartingDeckKnown           bool
	StartingHighProvision       int
}

func (s State) View(player int) (View, error) {
	if player < 0 || player > 1 {
		return View{}, fmt.Errorf("invalid viewer")
	}
	v := View{Ruleset: s.Ruleset, Round: s.Round, Turn: s.Turn, Revision: s.Revision, Wins: s.Wins, Passed: s.Passed, BetweenRounds: s.BetweenRounds, Finished: s.Finished, Winner: s.Winner, Score: [2]int{s.Score(0), s.Score(1)}, StartingDeckKnown: s.StartingDeckKnown[player], StartingHighProvision: s.StartingHighProvision[player]}
	for _, c := range s.Cards {
		if c.Owner < 0 || c.Owner > 1 {
			return View{}, fmt.Errorf("invalid owner")
		}
		switch c.Zone {
		case Hand:
			v.HandCount[c.Owner]++
			if c.Owner == player {
				v.Visible = append(v.Visible, c)
			}
		case Deck:
			v.DeckCount[c.Owner]++
		default:
			v.Visible = append(v.Visible, c)
		}
	}
	return v, nil
}

// Tracker is an offline adapter: State includes simulator secrets; callers must
// pass only ConfirmedView to a policy. It is not a live vision-state estimator.
type Tracker struct {
	engine     *Engine
	confirmed  State
	pending    *Event
	delivered  bool
	lastSource string
}

func NewTracker(e *Engine, initial State) *Tracker {
	return &Tracker{engine: e, confirmed: clone(initial)}
}
func (t *Tracker) ConfirmedView(player int) (View, error) { return t.confirmed.View(player) }
func (t *Tracker) Predict(a Action) (Event, error) {
	if t.pending != nil {
		return Event{}, fmt.Errorf("unresolved action; observe before retry")
	}
	_, event, err := t.engine.Apply(t.confirmed, a)
	if err != nil {
		return Event{}, err
	}
	saved := event
	saved.After = clone(event.After)
	t.pending = &saved
	return event, nil
}
func (t *Tracker) Delivered(sequence int) error {
	if t.pending == nil || t.pending.Sequence != sequence || t.delivered {
		return fmt.Errorf("unknown or duplicate delivery")
	}
	t.delivered = true
	return nil
}

// Confirm accepts a complete stable public observation, not transport success.
// On mismatch the prediction remains pending and confirmed state is unchanged.
func (t *Tracker) Confirm(player int, observed View, source string) error {
	if t.pending == nil || !t.delivered || source == "" || source == t.lastSource {
		return fmt.Errorf("missing delivery or fresh observation")
	}
	expected, err := t.pending.After.View(player)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(expected, observed) {
		return fmt.Errorf("observation mismatch: reconciliation required")
	}
	t.confirmed = clone(t.pending.After)
	t.pending = nil
	t.delivered = false
	t.lastSource = source
	return nil
}

// Reconcile applies independently confirmed event receipts, including opponent
// actions or draw outcomes, against the last confirmed state. No prediction is
// silently promoted and invalid batches leave the tracker unchanged.
func (t *Tracker) Reconcile(events []Event, source string) error {
	if len(events) == 0 || source == "" || source == t.lastSource {
		return fmt.Errorf("fresh evidence required")
	}
	state, err := t.engine.Replay(t.confirmed, events)
	if err != nil {
		return err
	}
	t.confirmed = state
	t.pending = nil
	t.delivered = false
	t.lastSource = source
	return nil
}
