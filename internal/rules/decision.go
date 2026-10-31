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
	MatchID, Phase string
	Player         int
	View           View
	Candidates     []Action
	Deadline       time.Time
}

type Proposal struct {
	MatchID, Phase string
	Revision       int
	Fingerprint    [32]byte
	Deadline       time.Time
	Action         Action
}

// DecisionService owns at most one cooperative analysis worker. It never controls
// the client. Close cancels analysis; workers examine only copied public data.
type DecisionService struct {
	engine     *Engine
	mu         sync.Mutex
	cancel     context.CancelFunc
	generation uint64
	result     *Proposal
	running    bool
}

func NewDecisionService(e *Engine) *DecisionService { return &DecisionService{engine: e} }

func copyInput(in DecisionInput) DecisionInput {
	in.View.Visible = append([]Instance(nil), in.View.Visible...)
	in.Candidates = append([]Action(nil), in.Candidates...)
	return in
}

func fingerprint(in DecisionInput) [32]byte {
	b, _ := json.Marshal(struct {
		MatchID, Phase string
		Player         int
		View           View
		Candidates     []Action
	}{in.MatchID, in.Phase, in.Player, in.View, in.Candidates})
	return sha256.Sum256(b)
}

// publicState constructs a legality-only state, never materializing unknown cards.
// Hidden counts are verified but intentionally do not influence Apply's decisions.
func (d *DecisionService) publicState(in DecisionInput) (State, error) {
	v := in.View
	if d.engine == nil || in.MatchID == "" || in.Phase != "turn" || in.Player < 0 || in.Player > 1 || v.Turn != in.Player || v.Finished || v.BetweenRounds || v.Ruleset != "native-v1" || v.HandCount[0] < 0 || v.HandCount[1] < 0 || v.DeckCount[0] < 0 || v.DeckCount[1] < 0 {
		return State{}, ErrNoDecision
	}
	s := State{Ruleset: v.Ruleset, Cards: append([]Instance(nil), v.Visible...), Turn: v.Turn, Round: v.Round, Revision: v.Revision, Wins: v.Wins, Passed: v.Passed, BetweenRounds: v.BetweenRounds, Finished: v.Finished, Winner: v.Winner}
	hand := 0
	for _, c := range s.Cards {
		if c.Zone == Deck || (c.Zone == Hand && c.Owner != in.Player) {
			return State{}, ErrNoDecision
		}
		if c.Zone == Hand {
			hand++
		}
	}
	if hand != v.HandCount[in.Player] || s.Score(0) != v.Score[0] || s.Score(1) != v.Score[1] {
		return State{}, ErrNoDecision
	}
	if err := d.engine.Validate(s); err != nil {
		return State{}, err
	}
	return s, nil
}

func (d *DecisionService) legal(in DecisionInput, s State, a Action) bool {
	if a.Player != in.Player || (a.Kind != "play" && a.Kind != "pass") {
		return false
	}
	for _, c := range in.Candidates {
		if c == a {
			_, _, err := d.engine.Apply(s, a)
			return err == nil
		}
	}
	return false
}

// Fast returns the first legal candidate in a deterministic ordering before the
// caller's deadline. It rejects expired or incomplete observations.
func (d *DecisionService) Fast(in DecisionInput) (Action, error) {
	if in.Deadline.IsZero() || !time.Now().Before(in.Deadline) {
		return Action{}, ErrNoDecision
	}
	s, err := d.publicState(in)
	if err != nil {
		return Action{}, err
	}
	candidates := append([]Action(nil), in.Candidates...)
	sort.Slice(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		if a.Kind != b.Kind {
			return a.Kind > b.Kind
		}
		if a.Card != b.Card {
			return a.Card < b.Card
		}
		if a.Target != b.Target {
			return a.Target < b.Target
		}
		return a.Row < b.Row
	})
	for _, a := range candidates {
		if !time.Now().Before(in.Deadline) {
			return Action{}, ErrNoDecision
		}
		if d.legal(in, s, a) && time.Now().Before(in.Deadline) {
			return a, nil
		}
	}
	return Action{}, ErrNoDecision
}

// Start cancels the preceding analysis and starts a bounded public-view-only
// evaluation. The worker is serialized: a restart never accumulates goroutines.
func (d *DecisionService) Start(in DecisionInput) error {
	in = copyInput(in)
	if in.Deadline.IsZero() || !time.Now().Before(in.Deadline) {
		return ErrNoDecision
	}
	if _, err := d.publicState(in); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.running {
		if d.cancel != nil {
			d.cancel()
			d.cancel = nil
		}
		d.generation++
		d.result = nil
		return errors.New("analysis cancellation in progress")
	}
	d.running = true
	d.generation++
	generation := d.generation
	d.result = nil
	ctx, cancel := context.WithDeadline(context.Background(), in.Deadline)
	d.cancel = cancel
	go d.analyze(ctx, generation, in)
	return nil
}

func (d *DecisionService) analyze(ctx context.Context, generation uint64, in DecisionInput) {
	defer func() {
		d.mu.Lock()
		if d.generation == generation || d.cancel == nil {
			d.running = false
		}
		if d.generation == generation && d.cancel != nil {
			d.cancel()
			d.cancel = nil
		}
		d.mu.Unlock()
	}()
	s, err := d.publicState(in)
	if err != nil {
		return
	}
	// Prefer retaining a card when already ahead after the opponent passed;
	// otherwise maximize immediate public score swing. Unknown enemy cards are
	// represented only by their counts, never guessed as known identities.
	best := Action{}
	bestScore := -int(^uint(0) >> 1)
	bestOutcome := -2
	found := false
	for _, a := range in.Candidates {
		select {
		case <-ctx.Done():
			return
		default:
		}
		if !d.legal(in, s, a) {
			continue
		}
		next, _, err := d.engine.Apply(s, a)
		if err != nil {
			continue
		}
		score := next.Score(in.Player) - next.Score(1-in.Player)
		outcome := 0
		if a.Kind == "pass" && s.Passed[1-in.Player] {
			score = s.Score(in.Player) - s.Score(1-in.Player)
			if score < 0 {
				outcome = -1
			} else if score > 0 {
				outcome = 1
			}
		}
		// An uncontested round win preserves every remaining card.
		if outcome == 1 {
			best, found = a, true
			break
		}
		if !found || outcome > bestOutcome || (outcome == bestOutcome && score > bestScore) {
			best, bestScore, bestOutcome, found = a, score, outcome, true
		}
	}
	if !found || ctx.Err() != nil || !time.Now().Before(in.Deadline) {
		return
	}
	p := &Proposal{MatchID: in.MatchID, Phase: in.Phase, Revision: in.View.Revision, Fingerprint: fingerprint(in), Deadline: in.Deadline, Action: best}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.generation == generation && ctx.Err() == nil && time.Now().Before(in.Deadline) {
		d.result = p
		d.running = false
		d.cancel()
		d.cancel = nil
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

func (d *DecisionService) Close() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.generation++
	if d.cancel != nil {
		d.cancel()
		d.cancel = nil
	}
	d.result = nil
}
