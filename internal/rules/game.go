package rules

import (
	"fmt"
	"reflect"
)

type Zone string

const (
	Hand      Zone = "hand"
	Deck      Zone = "deck"
	Board     Zone = "board"
	Graveyard Zone = "graveyard"
)

type Instance struct {
	ID                               string
	CardID, Owner, Power, Armor, Row int
	Zone                             Zone
}

// Native-v1 starts after mulligans, with an explicit hand and ordered deck.
// Round transitions await explicit observed/simulation draws and starter.
// This avoids inventing seasonal rules or a tie-round starter.
type State struct {
	Ruleset                 string
	Cards                   []Instance
	Turn, Round             int
	Wins                    [2]int
	Passed                  [2]bool
	BetweenRounds, Finished bool
	Winner                  int // -1 for ongoing or drawn match
	Revision                int
}

type Action struct {
	Kind         string // play, pass, start_round
	Player       int
	Card, Target string
	Row          int
	Draws        [2]int // start_round only: explicit external outcome
}

type Event struct {
	Sequence int
	Action   Action
	After    State // deterministic receipt; replay verifies rather than trusting it
}

func clone(s State) State { s.Cards = append([]Instance(nil), s.Cards...); return s }

func (e *Engine) Initial(hands, decks [2][]int, starter int) (State, error) {
	s := State{Ruleset: "native-v1", Turn: starter, Round: 1, Winner: -1}
	if starter < 0 || starter > 1 {
		return State{}, fmt.Errorf("invalid starter")
	}
	for p := 0; p < 2; p++ {
		if len(hands[p]) > 10 {
			return State{}, fmt.Errorf("hand exceeds ten")
		}
		for _, group := range []struct {
			zone Zone
			ids  []int
		}{{Hand, hands[p]}, {Deck, decks[p]}} {
			for _, id := range group.ids {
				if _, ok := e.effects[id]; !ok {
					return State{}, fmt.Errorf("unsupported card %d", id)
				}
				c := e.catalog.Cards[id]
				s.Cards = append(s.Cards, Instance{ID: fmt.Sprintf("p%d-%d", p, len(s.Cards)), CardID: id, Owner: p, Power: c.Attributes.Power, Armor: c.Attributes.Armor, Row: -1, Zone: group.zone})
			}
		}
	}
	return s, nil
}

func (s State) Score(p int) int {
	score := 0
	for _, c := range s.Cards {
		if c.Zone == Board && c.Owner == p {
			score += c.Power
		}
	}
	return score
}

func index(s State, id string) int {
	for i := range s.Cards {
		if s.Cards[i].ID == id {
			return i
		}
	}
	return -1
}

// Apply is atomic, including its returned event: failures return the original state.
func (e *Engine) Apply(s State, a Action) (State, Event, error) {
	if err := e.Validate(s); err != nil {
		return s, Event{}, err
	}
	next := clone(s)
	if err := e.apply(&next, a); err != nil {
		return s, Event{}, err
	}
	next.Revision++
	if err := e.Validate(next); err != nil {
		return s, Event{}, err
	}
	return next, Event{next.Revision, a, clone(next)}, nil
}

func (e *Engine) apply(s *State, a Action) error {
	if s.Ruleset != "native-v1" || s.Finished || a.Player < 0 || a.Player > 1 {
		return fmt.Errorf("invalid ruleset, player or finished match")
	}
	if a.Kind == "start_round" {
		if !s.BetweenRounds || a.Card != "" || a.Target != "" || a.Row != 0 {
			return fmt.Errorf("not a round transition")
		}
		for p, n := range a.Draws {
			if n < 0 || n > 3 {
				return fmt.Errorf("draw count outside baseline 0..3")
			}
			hand, deck := 0, 0
			for _, c := range s.Cards {
				if c.Owner == p {
					if c.Zone == Hand {
						hand++
					}
					if c.Zone == Deck {
						deck++
					}
				}
			}
			if hand+n > 10 || n > deck {
				return fmt.Errorf("invalid draw outcome")
			}
			for i := range s.Cards {
				if n > 0 && s.Cards[i].Owner == p && s.Cards[i].Zone == Deck {
					s.Cards[i].Zone = Hand
					n--
				}
			}
		}
		s.Turn = a.Player
		s.BetweenRounds = false
		s.Passed = [2]bool{}
		return nil
	}
	if s.BetweenRounds || a.Player != s.Turn || s.Passed[a.Player] || a.Draws != [2]int{} {
		return fmt.Errorf("not an active turn")
	}
	switch a.Kind {
	case "pass":
		if a.Card != "" || a.Target != "" || a.Row != 0 {
			return fmt.Errorf("pass has unexpected fields")
		}
		s.Passed[a.Player] = true
	case "play":
		i := index(*s, a.Card)
		if i < 0 || s.Cards[i].Owner != a.Player || s.Cards[i].Zone != Hand {
			return fmt.Errorf("card is not in player's hand")
		}
		d, ok := e.effects[s.Cards[i].CardID]
		if !ok {
			return fmt.Errorf("unsupported card")
		}
		c := e.catalog.Cards[d.ID]
		target := -1
		if d.Kind != "unit" {
			target = index(*s, a.Target)
			if target < 0 || (s.Cards[target].Zone != Board && !(d.Kind == "plunderer" && target == i)) {
				return fmt.Errorf("target must be a board unit")
			}
			if d.Kind == "piercing" && s.Cards[target].Owner == a.Player {
				return fmt.Errorf("target must be enemy")
			}
		} else if a.Target != "" {
			return fmt.Errorf("unit has no target")
		}
		if c.Attributes.Type == "Unit" {
			if a.Row < 0 || a.Row > 1 {
				return fmt.Errorf("invalid row")
			}
			count := 0
			for _, u := range s.Cards {
				if u.Owner == a.Player && u.Zone == Board && u.Row == a.Row {
					count++
				}
			}
			if count >= 9 {
				return fmt.Errorf("row full")
			}
			s.Cards[i].Zone = Board
			s.Cards[i].Row = a.Row
		} else {
			if a.Row != 0 {
				return fmt.Errorf("special has no row")
			}
			s.Cards[i].Zone = Graveyard
		}
		if target >= 0 {
			amount := d.Amount
			switch d.Kind {
			case "boost":
				s.Cards[target].Power += amount
			default:
				if d.Kind == "stunning" && s.Cards[target].Armor > 0 {
					amount = 7
				}
				if d.Kind != "piercing" {
					absorbed := min(amount, s.Cards[target].Armor)
					s.Cards[target].Armor -= absorbed
					amount -= absorbed
				}
				s.Cards[target].Power -= amount
				if s.Cards[target].Power <= 0 {
					s.Cards[target].Power = 0
					s.Cards[target].Zone = Graveyard
					s.Cards[target].Row = -1
				}
				if d.Kind == "plunderer" && s.Cards[target].Owner == a.Player {
					s.Cards[i].Power += 4
				}
			}
		}
	default:
		return fmt.Errorf("unknown action %q", a.Kind)
	}
	if s.Passed[0] && s.Passed[1] {
		e.endRound(s)
	} else if !s.Passed[1-a.Player] {
		s.Turn = 1 - a.Player
	}
	return nil
}

func (e *Engine) endRound(s *State) {
	x, y := s.Score(0), s.Score(1)
	// Explicit offline profile: a tie credits both players one round.
	if x >= y {
		s.Wins[0]++
	}
	if y >= x {
		s.Wins[1]++
	}
	if s.Wins[0] >= 2 || s.Wins[1] >= 2 || s.Round == 3 {
		s.Finished = true
		s.Winner = -1
		if s.Wins[0] > s.Wins[1] {
			s.Winner = 0
		}
		if s.Wins[1] > s.Wins[0] {
			s.Winner = 1
		}
		return
	}
	for i := range s.Cards {
		if s.Cards[i].Zone == Board {
			s.Cards[i].Zone = Graveyard
			s.Cards[i].Row = -1
		}
	}
	s.Round++
	s.BetweenRounds = true
}

func (e *Engine) Replay(initial State, events []Event) (State, error) {
	if err := e.Validate(initial); err != nil {
		return State{}, err
	}
	s := clone(initial)
	for _, event := range events {
		next, receipt, err := e.Apply(s, event.Action)
		if err != nil {
			return State{}, err
		}
		if !reflect.DeepEqual(receipt, event) {
			return State{}, fmt.Errorf("event %d diverged", event.Sequence)
		}
		s = next
	}
	return s, nil
}
