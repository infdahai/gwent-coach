package rules

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"sort"
	"sync"
	"time"
)

var ErrNoDecision = errors.New("no legal decision before deadline")

// DecisionInput is a confirmed public view and caller-supplied candidate actions.
// MatchID and Phase identify the observation independently of its local revision.
// Deadline must originate from time.Now().Add so it retains monotonic time.
type DecisionInput struct {
	MatchID, Phase     string
	Player             int
	View               View
	HandOrder          []string // confirmed hand instance IDs, left to right
	Candidates         []Action
	Deadline           time.Time
	AllowPassWithCards bool
}

type Proposal struct {
	MatchID, Phase string
	Revision       int
	Fingerprint    [32]byte
	Deadline       time.Time
	Action         Action
}

// DecisionService owns one session-long cooperative analysis goroutine. It never
// controls the client. Close waits for it to exit; it examines copied public data.
type DecisionService struct {
	engine     *Engine
	mu         sync.Mutex
	cancel     context.CancelFunc
	generation uint64
	result     *Proposal
	pending    *DecisionInput
	current    *DecisionInput
	wake       chan struct{}
	done       chan struct{}
	closed     bool
}

func NewDecisionService(e *Engine) *DecisionService {
	d := &DecisionService{engine: e, wake: make(chan struct{}, 1), done: make(chan struct{})}
	go d.run()
	return d
}

func copyInput(in DecisionInput) DecisionInput {
	in.View.Visible = append([]Instance(nil), in.View.Visible...)
	in.HandOrder = append([]string(nil), in.HandOrder...)
	in.Candidates = append([]Action(nil), in.Candidates...)
	return in
}

func fingerprint(in DecisionInput) [32]byte {
	b, _ := json.Marshal(struct {
		MatchID, Phase     string
		Player             int
		View               View
		HandOrder          []string
		Candidates         []Action
		AllowPassWithCards bool
	}{in.MatchID, in.Phase, in.Player, in.View, in.HandOrder, in.Candidates, in.AllowPassWithCards})
	return sha256.Sum256(b)
}

// publicState constructs a legality-only state, never materializing unknown cards.
// Hidden counts are verified but intentionally do not influence Apply's decisions.
func (d *Evaluator) publicState(in DecisionInput) (State, error) {
	v := in.View
	if d.engine == nil || in.MatchID == "" || in.Phase != "turn" || in.Player < 0 || in.Player > 1 || v.Turn != in.Player || v.Finished || v.BetweenRounds || v.Ruleset != "native-v1" || v.HandCount[0] < 0 || v.HandCount[1] < 0 || v.DeckCount[0] < 0 || v.DeckCount[1] < 0 {
		return State{}, ErrNoDecision
	}
	s := State{Ruleset: v.Ruleset, Cards: append([]Instance(nil), v.Visible...), Turn: v.Turn, Round: v.Round, Revision: v.Revision, Wins: v.Wins, Passed: v.Passed, BetweenRounds: v.BetweenRounds, Finished: v.Finished, Winner: v.Winner}
	s.StartingDeckKnown[in.Player], s.StartingHighProvision[in.Player] = v.StartingDeckKnown, v.StartingHighProvision
	hand := 0
	ids := make(map[string]bool, len(s.Cards))
	for _, c := range s.Cards {
		if c.ID == "" || ids[c.ID] || c.Owner < 0 || c.Owner > 1 {
			return State{}, ErrNoDecision
		}
		ids[c.ID] = true
		if c.Zone == Deck || (c.Zone == Hand && c.Owner != in.Player) {
			return State{}, ErrNoDecision
		}
		if c.Zone == Hand {
			hand++
		}
	}
	if hand > 10 || hand != v.HandCount[in.Player] || hand != len(in.HandOrder) || s.Score(0) != v.Score[0] || s.Score(1) != v.Score[1] {
		return State{}, ErrNoDecision
	}
	seen := make(map[string]bool, hand)
	for _, id := range in.HandOrder {
		i := index(s, id)
		if i < 0 || seen[id] || s.Cards[i].Owner != in.Player || s.Cards[i].Zone != Hand {
			return State{}, ErrNoDecision
		}
		seen[id] = true
	}
	// Identified but unsupported cards may remain in our hand. They still count
	// toward completeness, but cannot become legal candidates or board units.
	projected := s.Cards[:0]
	for _, card := range s.Cards {
		if card.CardID == 0 && card.Zone == Graveyard {
			if card.Row != -1 || card.Power != 0 || card.Armor != 0 {
				return State{}, ErrNoDecision
			}
			continue
		}
		if card.CardID == 0 && card.Zone == Hand && card.Owner == in.Player {
			if card.Row != -1 || card.Power != 0 || card.Armor != 0 {
				return State{}, ErrNoDecision
			}
			continue
		}
		// Unknown enemy objects retain their observed power and armor; their
		// effects are not projected into the deterministic rules engine.
		if card.CardID == 0 && card.Zone == Board && card.Owner != in.Player {
			if card.Row < 0 || card.Row > 1 || card.Power < 0 || card.Power > 1000000 || card.Armor < 0 || card.Armor > 1000000 {
				return State{}, ErrNoDecision
			}
			continue
		}
		if _, supported := d.engine.effects[card.CardID]; !supported && card.Zone == Hand && card.Owner == in.Player {
			base, known := d.engine.catalog.Cards[card.CardID]
			if !known || card.Row != -1 || card.Power != base.Attributes.Power || card.Armor != base.Attributes.Armor {
				return State{}, ErrNoDecision
			}
			continue
		}
		projected = append(projected, card)
	}
	s.Cards = projected
	if err := d.engine.Validate(s); err != nil {
		return State{}, err
	}
	return s, nil
}

func hasOpaqueCards(in DecisionInput) bool {
	for _, card := range in.View.Visible {
		if card.CardID == 0 && (card.Zone == Hand || card.Zone == Board || card.Zone == Graveyard) {
			return true
		}
	}
	return false
}

func hasUnknownEnemyObject(in DecisionInput) bool {
	for _, card := range in.View.Visible {
		if card.CardID == 0 && card.Zone == Board && card.Owner != in.Player && card.Power == 0 {
			return true
		}
	}
	return false
}

func safeMageTarget(in DecisionInput, a Action) bool {
	if a.Kind != "play" || a.Target == "" {
		return false
	}
	mage, target := false, false
	for _, card := range in.View.Visible {
		if card.ID == a.Card && card.Owner == in.Player && card.Zone == Hand && card.CardID == 202908 {
			mage = true
		}
		if card.ID == a.Target && card.Owner != in.Player && card.Zone == Board && card.CardID == 200535 {
			target = true
		}
		if card.Zone == Board && card.CardID != 132403 && card.CardID != 200535 && card.CardID != 142102 && card.CardID != 202140 {
			return false
		}
	}
	return mage && target
}

func safeScholar(in DecisionInput, a Action) bool {
	if !in.View.StartingDeckKnown || a.Kind != "play" || a.Target != "" {
		return false
	}
	found := false
	for _, card := range in.View.Visible {
		if card.ID == a.Card && card.Owner == in.Player && card.Zone == Hand && card.CardID == 202218 {
			found = true
		}
		if card.Zone == Board {
			if card.Owner == in.Player && card.CardID != 132403 && card.CardID != 202908 {
				return false
			}
			if card.Owner != in.Player && card.CardID != 200535 && card.CardID != 142102 {
				return false
			}
		}
	}
	return found
}

func (d *DecisionService) legal(in DecisionInput, s State, a Action) bool {
	for _, candidate := range in.Candidates {
		if candidate == a {
			return NewEvaluator(d.engine).validateAction(in, s, a) == nil
		}
	}
	return false
}

// Candidates enumerates actions supported by the offline rules engine from a
// complete public view. It does not establish that a live observation is correct
// or that an action can be executed in the client.
func (d *Evaluator) Candidates(in DecisionInput) ([]Action, error) {
	s, err := d.publicState(in)
	if err != nil {
		return nil, err
	}
	if in.Deadline.IsZero() || !time.Now().Before(in.Deadline) {
		return nil, ErrNoDecision
	}
	candidates := []Action{{Kind: "pass", Player: in.Player}}
	for _, card := range s.Cards {
		if card.Owner == in.Player && card.Zone == Board && card.CardID == 202140 {
			for _, target := range s.Cards {
				if target.Owner == in.Player && target.Zone == Board && d.engine.catalog.Cards[target.CardID].Attributes.Type == "Unit" {
					candidates = append(candidates, Action{Kind: "order", Player: in.Player, Card: card.ID, Target: target.ID})
				}
			}
		}
	}
	for _, card := range s.Cards {
		if card.Owner != in.Player || card.Zone != Hand {
			continue
		}
		if hasOpaqueCards(in) && card.CardID != 132403 && card.CardID != 202908 && card.CardID != 202218 {
			continue
		}
		effect := d.engine.effects[card.CardID]
		rows := []int{0}
		if d.engine.catalog.Cards[card.CardID].Attributes.Type == "Unit" {
			rows = []int{0, 1}
		}
		targets := []string{""}
		if effect.Kind != "unit" && effect.Kind != "wolf_pack" && effect.Kind != "keeper_empty_board" && effect.Kind != "oxenfurt_scholar" {
			targets = nil
			for _, target := range s.Cards {
				if target.Zone == Board || (effect.Kind == "plunderer" && target.ID == card.ID) {
					targets = append(targets, target.ID)
				}
			}
		}
		for _, row := range rows {
			for _, target := range targets {
				candidates = append(candidates, Action{Kind: "play", Player: in.Player, Card: card.ID, Target: target, Row: row})
			}
		}
	}
	legal := make([]Action, 0, len(candidates))
	for _, candidate := range candidates {
		if !time.Now().Before(in.Deadline) {
			return nil, ErrNoDecision
		}
		if hasUnknownEnemyObject(in) && candidate.Kind != "pass" {
			continue
		}
		if hasOpaqueCards(in) && candidate.Kind == "pass" && (!in.AllowPassWithCards || in.View.Passed[1-in.Player]) {
			continue
		}
		if hasOpaqueCards(in) && candidate.Kind == "play" && candidate.Target != "" && !safeMageTarget(in, candidate) {
			continue
		}
		if hasOpaqueCards(in) && candidate.Kind == "play" && candidate.Target == "" {
			i := index(s, candidate.Card)
			if i < 0 || (s.Cards[i].CardID != 132403 && !safeScholar(in, candidate)) {
				continue
			}
		}
		if err := d.validateAction(in, s, candidate); err == nil {
			legal = append(legal, candidate)
		}
	}
	return legal, nil
}

// Fast chooses the leftmost confirmed hand card with a supplied legal action.
// Opaque slots remain unidentified and restrict the supported actions.
func (d *DecisionService) Fast(in DecisionInput) (Action, error) {
	return d.choose(context.Background(), in)
}

func (d *DecisionService) choose(ctx context.Context, in DecisionInput) (Action, error) {
	if in.Deadline.IsZero() || !time.Now().Before(in.Deadline) {
		return Action{}, ErrNoDecision
	}
	s, err := d.publicState(in)
	if err != nil {
		return Action{}, err
	}
	candidates := append([]Action(nil), in.Candidates...)
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].Target != candidates[j].Target {
			return candidates[i].Target < candidates[j].Target
		}
		return candidates[i].Row < candidates[j].Row
	})
	for _, candidate := range candidates {
		if candidate.Kind == "order" && d.legal(in, s, candidate) && ctx.Err() == nil && time.Now().Before(in.Deadline) {
			return candidate, nil
		}
	}
	order := in.HandOrder
	if len(order) == 0 {
		order = []string{""}
	}
	for _, id := range order {
		for _, a := range candidates {
			if ctx.Err() != nil || !time.Now().Before(in.Deadline) {
				return Action{}, ErrNoDecision
			}
			if (id == "" && a.Kind != "pass") || (id != "" && (a.Kind != "play" || a.Card != id)) {
				continue
			}
			if d.legal(in, s, a) && ctx.Err() == nil && time.Now().Before(in.Deadline) {
				return a, nil
			}
		}
	}
	if len(in.HandOrder) > 0 && in.AllowPassWithCards {
		for _, candidate := range candidates {
			if candidate.Kind == "pass" && d.legal(in, s, candidate) && ctx.Err() == nil && time.Now().Before(in.Deadline) {
				return candidate, nil
			}
		}
	}
	return Action{}, ErrNoDecision
}

// Invalidate discards all analysis for an observation that is no longer usable.
// The worker remains available for a later confirmed observation.
func (d *DecisionService) Invalidate() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.invalidateLocked()
}

func (d *DecisionService) invalidateLocked() {
	d.generation++
	d.result = nil
	d.pending = nil
	d.current = nil
	if d.cancel != nil {
		d.cancel()
	}
}

// Start replaces any ongoing or queued analysis with the latest public view.
// Only one analysis runs at a time; intermediate queued views are discarded.
func (d *DecisionService) Start(in DecisionInput) error {
	in = copyInput(in)
	var inputErr error
	if in.Deadline.IsZero() || !time.Now().Before(in.Deadline) {
		inputErr = ErrNoDecision
	} else {
		_, inputErr = d.publicState(in)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return errors.New("decision service closed")
	}
	if inputErr == nil && d.current != nil && fingerprint(*d.current) == fingerprint(in) {
		if !d.current.Deadline.Equal(in.Deadline) {
			return errors.New("deadline changed for the same observation")
		}
		return nil
	}
	d.invalidateLocked()
	if inputErr != nil {
		return inputErr
	}
	d.current = &in
	d.pending = &in
	select {
	case d.wake <- struct{}{}:
	default:
	}
	return nil
}

func (d *DecisionService) run() {
	defer close(d.done)
	for range d.wake {
		for {
			d.mu.Lock()
			if d.closed {
				d.mu.Unlock()
				return
			}
			if d.pending == nil {
				d.mu.Unlock()
				break
			}
			in := *d.pending
			d.pending = nil
			generation := d.generation
			ctx, cancel := context.WithDeadline(context.Background(), in.Deadline)
			d.cancel = cancel
			d.mu.Unlock()
			d.analyze(ctx, generation, in)
			cancel()
			d.mu.Lock()
			d.cancel = nil
			d.mu.Unlock()
		}
	}
}

func (d *DecisionService) analyze(ctx context.Context, generation uint64, in DecisionInput) {
	best, err := d.choose(ctx, in)
	if err != nil || ctx.Err() != nil || !time.Now().Before(in.Deadline) {
		return
	}
	p := &Proposal{MatchID: in.MatchID, Phase: in.Phase, Revision: in.View.Revision, Fingerprint: fingerprint(in), Deadline: in.Deadline, Action: best}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.generation == generation && ctx.Err() == nil && time.Now().Before(in.Deadline) {
		d.result = p
	}
}

// Poll accepts only a completed result matching the current confirmed observation,
// candidate set and original deadline; legality is checked again on receipt.
func (d *DecisionService) Poll(in DecisionInput) (Action, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	p := d.result
	if p == nil || p.MatchID != in.MatchID || p.Phase != in.Phase || p.Revision != in.View.Revision || p.Fingerprint != fingerprint(in) || !p.Deadline.Equal(in.Deadline) || !time.Now().Before(p.Deadline) {
		return Action{}, ErrNoDecision
	}
	s, err := d.publicState(in)
	if err != nil || !d.legal(in, s, p.Action) || !time.Now().Before(p.Deadline) {
		return Action{}, ErrNoDecision
	}
	d.result = nil
	return p.Action, nil
}

// Close cancels pending work and waits for the analysis worker to exit.
// A closed service cannot be restarted.
func (d *DecisionService) Close() {
	d.mu.Lock()
	d.closed = true
	d.invalidateLocked()
	select {
	case d.wake <- struct{}{}:
	default:
	}
	done := d.done
	d.mu.Unlock()
	if done != nil {
		<-done
	}
}
