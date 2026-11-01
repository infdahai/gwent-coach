package strategy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
)

// RankerPayload specifies additive, bounded integer weights for native actions.
// Unspecified weights are zero and ties retain the supplied baseline order.
type RankerPayload struct {
	Kind        string           `json:"kind"`
	CardWeights map[string]int32 `json:"card_weights"`
	KindWeights map[string]int32 `json:"kind_weights"`
}

type actionRanker struct{ payload RankerPayload }

// NewRanker validates the executable payload before it can be selected.
func NewRanker(payload []byte) (Policy, error) {
	var p RankerPayload
	dec := json.NewDecoder(bytes.NewReader(payload))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return nil, fmt.Errorf("decode ranker: %w", err)
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return nil, errors.New("ranker contains trailing data")
	}
	if p.Kind != "action-ranker-v1" {
		return nil, fmt.Errorf("unsupported policy kind %q", p.Kind)
	}
	for k := range p.CardWeights {
		if k == "" {
			return nil, errors.New("empty card weight key")
		}
	}
	for k := range p.KindWeights {
		if k != "play" && k != "pass" {
			return nil, fmt.Errorf("unsupported action kind %q", k)
		}
	}
	return &actionRanker{payload: p}, nil
}

// Choose uses the ordered candidate snapshot; malformed or unsupported actions
// fail the proposal so the caller can use the regular validated fallback.
func (r *actionRanker) Choose(ctx context.Context, o Observation) (string, error) {
	best := ""
	var bestScore int64
	for _, id := range o.Candidates {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		var action struct {
			Kind string
			Card string
		}
		if err := json.Unmarshal([]byte(id), &action); err != nil {
			return "", fmt.Errorf("decode candidate: %w", err)
		}
		if action.Kind != "play" && action.Kind != "pass" {
			return "", fmt.Errorf("unsupported candidate kind %q", action.Kind)
		}
		score := int64(r.payload.KindWeights[action.Kind])
		if action.Kind == "play" {
			score += int64(r.payload.CardWeights[action.Card])
		}
		if best == "" || score > bestScore {
			best, bestScore = id, score
		}
	}
	if best == "" {
		return "", ErrNoDecision
	}
	return best, nil
}

// MatchPolicy is a snapshot that must be retained for the entire match.
type MatchPolicy struct {
	Version string
	Policy  Policy
}

// ExecutablePolicies retains an always-available baseline and accepts only
// compatible, executable candidate artifacts for future matches.
type ExecutablePolicies struct {
	mu         sync.Mutex
	compatible Compatibility
	selected   MatchPolicy
}

func NewExecutablePolicies(baselineVersion string, baseline Policy, compatible Compatibility) (*ExecutablePolicies, error) {
	if baselineVersion == "" || compatible.RulesVersion == "" || compatible.CatalogVersion == "" {
		return nil, errors.New("baseline and compatibility versions required")
	}
	return &ExecutablePolicies{compatible: compatible, selected: MatchPolicy{Version: baselineVersion, Policy: baseline}}, nil
}

func (p *ExecutablePolicies) Select(data []byte) error {
	a, err := LoadArtifact(data, p.compatible)
	if err != nil {
		return fmt.Errorf("candidate rejected: %w", err)
	}
	policy, err := NewRanker(a.Payload)
	if err != nil {
		return fmt.Errorf("candidate rejected: %w", err)
	}
	p.mu.Lock()
	p.selected = MatchPolicy{Version: a.PolicyVersion, Policy: policy}
	p.mu.Unlock()
	return nil
}

func (p *ExecutablePolicies) BeginMatch() MatchPolicy {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.selected
}
