package rules

import "fmt"

// Validate protects replay/import boundaries. It checks structural invariants;
// it does not authenticate an observation or prove historical reachability.
func (e *Engine) Validate(s State) error {
	if s.Ruleset != "native-v1" || s.Turn < 0 || s.Turn > 1 || s.Round < 1 || s.Round > 3 || s.Revision < 0 || s.Winner < -1 || s.Winner > 1 {
		return fmt.Errorf("invalid state header")
	}
	if s.Wins[0] < 0 || s.Wins[1] < 0 || s.Wins[0] > 2 || s.Wins[1] > 2 {
		return fmt.Errorf("invalid round wins")
	}
	if s.Finished && s.BetweenRounds || !s.Finished && s.Winner != -1 {
		return fmt.Errorf("invalid match phase")
	}
	terminal := s.Wins[0] == 2 || s.Wins[1] == 2
	winner := -1
	if terminal {
		if s.Wins[0] > s.Wins[1] {
			winner = 0
		}
		if s.Wins[1] > s.Wins[0] {
			winner = 1
		}
	}
	if s.Finished != terminal || s.Winner != winner {
		return fmt.Errorf("wins and terminal result disagree")
	}
	if s.BetweenRounds && (s.Round == 1 || !s.Passed[0] || !s.Passed[1]) {
		return fmt.Errorf("invalid round boundary")
	}
	if s.Finished && (!s.Passed[0] || !s.Passed[1]) {
		return fmt.Errorf("finished without passes")
	}
	if !s.Finished && !s.BetweenRounds && (s.Passed[s.Turn] || s.Wins[0] >= s.Round || s.Wins[1] >= s.Round) {
		return fmt.Errorf("invalid active round")
	}
	seen := map[string]bool{}
	var hands [2]int
	var rows [2][2]int
	for _, c := range s.Cards {
		if c.ID == "" || seen[c.ID] || c.Owner < 0 || c.Owner > 1 || c.Power < 0 || c.Power > 1000000 || c.Armor < 0 || c.Armor > 1000000 {
			return fmt.Errorf("invalid instance %q", c.ID)
		}
		seen[c.ID] = true
		if _, ok := e.effects[c.CardID]; !ok {
			return fmt.Errorf("unsupported instance %d", c.CardID)
		}
		if c.Zone == Hand || c.Zone == Deck {
			base := e.catalog.Cards[c.CardID]
			if c.Power != base.Attributes.Power || c.Armor != base.Attributes.Armor {
				return fmt.Errorf("off-board modifications unsupported")
			}
		}
		switch c.Zone {
		case Board:
			if c.Row < 0 || c.Row > 1 || c.Power == 0 || e.catalog.Cards[c.CardID].Attributes.Type != "Unit" {
				return fmt.Errorf("invalid board unit")
			}
			rows[c.Owner][c.Row]++
			if rows[c.Owner][c.Row] > 9 {
				return fmt.Errorf("row full")
			}
		case Hand:
			hands[c.Owner]++
			if hands[c.Owner] > 10 {
				return fmt.Errorf("hand full")
			}
			if c.Row != -1 {
				return fmt.Errorf("hand row")
			}
		case Deck, Graveyard:
			if c.Row != -1 {
				return fmt.Errorf("off-board row")
			}
		default:
			return fmt.Errorf("unknown zone")
		}
	}
	return nil
}
