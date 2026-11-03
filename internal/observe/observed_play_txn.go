package observe

// observedPlayChannel is the shared match channel. The lifecycle and every
// card play observe, deliver and confirm through the same closures so input
// timestamps, two-frame freshness, receipts, STOP/cancel handling and the
// no-replay rule hold uniformly.
type observedPlayChannel struct {
	await   func(playID string, expected, waiting []string) ([2]observedMatchFrame, error)
	send    func([2]observedMatchFrame, observedMatchInput) error
	confirm func(name string, pair [2]observedMatchFrame) error
}

func observedClick(name string, x, y int, playID string) observedMatchInput {
	return observedMatchInput{Name: name, Action: Action{Kind: "click", X: x, Y: y}, PlayID: playID}
}

// playSelectedCard executes the action phases for one already selected safe
// profile: select, place, semantic confirmation, and an explicit EndTurn only
// when the game did not end the turn automatically. It never chooses a profile
// and never replays an input.
//
// plays grows only after the placement itself is confirmed by a fresh pair of
// post-delivery frames, so an unverified or partially delivered placement
// cannot qualify the match.
func playSelectedCard(ch observedPlayChannel, cfg ObservedMatchConfig, pair [2]observedMatchFrame, play ObservedPlay, slot int, identified bool, plays *int) ([2]observedMatchFrame, error) {
	selectInput := observedClick("select_card", cfg.Hand.Clicks[slot].X, cfg.Hand.Clicks[slot].Y, play.ID)
	selectInput.Slot, selectInput.Identified = slot, identified
	if err := ch.send(pair, selectInput); err != nil {
		return pair, err
	}
	var err error
	if pair, err = ch.await(play.ID, []string{"selected"}, []string{"ready"}); err != nil {
		return pair, err
	}
	if err = ch.confirm("select_card", pair); err != nil {
		return pair, err
	}
	if err = ch.send(pair, observedClick("place_card", play.Row.X, play.Row.Y, play.ID)); err != nil {
		return pair, err
	}
	if pair, err = ch.await(play.ID, []string{"placed", "ended"}, []string{"selected"}); err != nil {
		return pair, err
	}
	(*plays)++
	if err = ch.confirm("card_play", pair); err != nil {
		return pair, err
	}
	if pair[1].Plays[play.ID] == "placed" {
		// The card landed but the game did not end the turn (for example while
		// a deploy effect resolves), so the explicit EndTurn input is required.
		if err = ch.send(pair, observedClick("end_turn", cfg.EndTurn.X, cfg.EndTurn.Y, play.ID)); err != nil {
			return pair, err
		}
		if pair, err = ch.await(play.ID, []string{"ended"}, []string{"placed"}); err != nil {
			return pair, err
		}
	}
	if err = ch.confirm("turn_end", pair); err != nil {
		return pair, err
	}
	return pair, nil
}
