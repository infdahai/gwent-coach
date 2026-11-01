package observe

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"gwentcoachmvp/internal/rules"
	"gwentcoachmvp/internal/strategy"
)

// ActionEvaluator is the synchronous rules boundary; it owns no strategy worker.
type ActionEvaluator interface {
	Candidates(rules.DecisionInput) ([]rules.Action, error)
	ValidateAction(rules.DecisionInput, rules.Action) error
}

func chooseFallback(evaluator ActionEvaluator, in rules.DecisionInput) (rules.Action, error) {
	for _, action := range rules.OrderCandidates(in) {
		if !time.Now().Before(in.Deadline) {
			break
		}
		if action.Kind == "pass" && len(in.HandOrder) > 0 && !in.AllowPassWithCards {
			continue
		}
		if evaluator.ValidateAction(in, action) == nil {
			return action, nil
		}
	}
	return rules.Action{}, rules.ErrNoDecision
}

// StrategyPolicy adapts the strategy worker to LocalLoop's serialized policy
// interface. The rules service remains the source of candidates and legality.
type StrategyPolicy struct {
	decisions   ActionEvaluator
	policy      strategy.Policy
	service     *strategy.Service
	reserve     time.Duration
	match       string
	observation strategy.Observation
	input       []byte
	actions     map[string]rules.Action
	fallbackAt  time.Time
}

func NewStrategyPolicy(decisions ActionEvaluator, policy strategy.Policy, executionReserve time.Duration) (*StrategyPolicy, error) {
	if decisions == nil || executionReserve <= 0 {
		return nil, fmt.Errorf("rules service and positive execution reserve required")
	}
	return &StrategyPolicy{decisions: decisions, policy: policy, reserve: executionReserve}, nil
}

func (p *StrategyPolicy) Start(in rules.DecisionInput, version uint64) error {
	encoded, err := json.Marshal(in)
	if err != nil {
		return err
	}
	if version == 0 {
		if p.service != nil {
			p.service.Invalidate()
		}
		return strategy.ErrObservation
	}
	if p.service != nil && p.match == in.MatchID && version <= p.observation.Revision {
		if version == p.observation.Revision && bytes.Equal(encoded, p.input) && in.Deadline == p.observation.Deadline {
			return p.service.Start(p.observation)
		}
		p.service.Invalidate()
		return strategy.ErrObservation
	}
	if p.service == nil || p.match != in.MatchID {
		if p.service != nil {
			p.service.Close()
		}
		p.service = strategy.New(p.policy)
		p.match = in.MatchID
	}
	ordered := rules.OrderCandidates(in)
	ids := make([]string, 0, len(ordered))
	actions := make(map[string]rules.Action, len(ordered))
	for _, action := range ordered {
		if action.Kind == "pass" && len(in.HandOrder) > 0 && !in.AllowPassWithCards {
			continue
		}
		b, e := json.Marshal(action)
		if e != nil {
			return e
		}
		id := string(b)
		if _, exists := actions[id]; exists {
			continue
		}
		actions[id] = action
		ids = append(ids, id)
	}
	state, err := json.Marshal(struct {
		Phase     string
		Player    int
		View      rules.View
		HandOrder []string
	}{in.Phase, in.Player, in.View, in.HandOrder})
	if err != nil {
		return err
	}
	o := strategy.Observation{MatchID: in.MatchID, Revision: version, Deadline: in.Deadline, State: state, Candidates: ids}
	if err = p.service.Start(o); err != nil {
		return err
	}
	p.observation, p.input, p.actions = o, encoded, actions
	p.fallbackAt = in.Deadline.Add(-p.reserve)
	if p.policy == nil {
		p.fallbackAt = time.Now()
	}
	return nil
}

func (p *StrategyPolicy) Poll(in rules.DecisionInput) (rules.Action, error) {
	encoded, err := json.Marshal(in)
	if err != nil {
		return rules.Action{}, err
	}
	if p.service == nil || !bytes.Equal(encoded, p.input) || in.Deadline != p.observation.Deadline {
		return rules.Action{}, rules.ErrNoDecision
	}
	validate := func(id string) bool {
		action, ok := p.actions[id]
		if !ok {
			return false
		}
		return p.decisions.ValidateAction(in, action) == nil
	}
	id, err := p.service.Poll(p.observation, validate)
	if errors.Is(err, strategy.ErrNoDecision) && !time.Now().Before(p.fallbackAt) {
		id, err = p.service.ResolveFallback(p.observation, validate)
	}
	if errors.Is(err, strategy.ErrNoDecision) {
		return rules.Action{}, rules.ErrNoDecision
	}
	if err != nil {
		return rules.Action{}, err
	}
	return p.actions[id], nil
}

func (p *StrategyPolicy) Invalidate() {
	if p.service != nil {
		p.service.Invalidate()
	}
}
func (p *StrategyPolicy) Close() {
	if p.service != nil {
		p.service.Close()
		p.service = nil
	}
}
