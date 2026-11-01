// Package strategy coordinates asynchronous policy proposals independently of game rules.
package strategy

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"sync"
	"time"
)

var ErrNoDecision = errors.New("no decision available")
var ErrClosed = errors.New("strategy service closed")
var ErrObservation = errors.New("invalid or conflicting observation")

// Observation is an immutable-by-convention snapshot. Candidates are unique IDs
// in fallback priority order, generated for this observation by the caller.
// Revision increases within a match, including after invalidation. State is
// caller-defined public observation data for the selected policy. Deadline must
// be made from time.Now().Add and retained unchanged for the revision.
type Observation struct {
	MatchID       string
	PolicyVersion string
	Revision      uint64
	Deadline      time.Time
	State         []byte
	Candidates    []string
}

// Policy may inspect the copied observation and choose a candidate ID. It must
// honor cancellation; it must not retain or mutate the observation after returning.
type Policy interface {
	Choose(context.Context, Observation) (string, error)
}

// Service accepts one match's ordered observations. Calls to Start and Invalidate
// must be serialized in observation order. Validate and execution are caller-owned.
type Service struct {
	mu            sync.Mutex
	policy        Policy
	policyVersion string
	match         string
	last          *Observation
	active        bool
	consumed      bool
	generation    uint64
	pending       *Observation
	result        string
	cancel        context.CancelFunc
	wake          chan struct{}
	done          chan struct{}
	closed        bool
}

// New starts one session worker. A nil policy uses the ordered fallback only.
func New(policy Policy) *Service {
	return NewVersioned(policy, "")
}

// NewVersioned binds a service and all its observations to a policy version for one match.
func NewVersioned(policy Policy, version string) *Service {
	s := &Service{policy: policy, policyVersion: version, wake: make(chan struct{}, 1), done: make(chan struct{})}
	go s.run()
	return s
}

func clone(o Observation) Observation {
	o.State = slices.Clone(o.State)
	o.Candidates = slices.Clone(o.Candidates)
	return o
}

func same(a, b Observation) bool {
	return a.MatchID == b.MatchID && a.PolicyVersion == b.PolicyVersion && a.Revision == b.Revision && a.Deadline == b.Deadline && bytes.Equal(a.State, b.State) && slices.Equal(a.Candidates, b.Candidates)
}

func valid(o Observation) bool {
	if o.MatchID == "" || o.Revision == 0 || o.Deadline.IsZero() || !time.Now().Before(o.Deadline) || len(o.Candidates) == 0 {
		return false
	}
	seen := make(map[string]bool, len(o.Candidates))
	for _, c := range o.Candidates {
		if c == "" || seen[c] {
			return false
		}
		seen[c] = true
	}
	return true
}

func (s *Service) invalidateLocked() {
	s.generation++
	s.active = false
	s.pending = nil
	s.result = ""
	if s.cancel != nil {
		s.cancel()
	}
}

// Start submits the newest observation. A repeated identical revision is a
// no-op; conflicting or older revisions are rejected and invalidate current work.
func (s *Service) Start(o Observation) error {
	o = clone(o)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	if !valid(o) || o.PolicyVersion != s.policyVersion || (s.match != "" && o.MatchID != s.match) {
		s.invalidateLocked()
		return ErrObservation
	}
	if s.last != nil && o.Revision <= s.last.Revision {
		if same(*s.last, o) {
			return nil
		}
		s.invalidateLocked()
		return ErrObservation
	}
	s.invalidateLocked()
	s.match = o.MatchID
	s.last = &o
	s.active = true
	s.consumed = false
	if s.policy != nil {
		s.pending = &o
		select {
		case s.wake <- struct{}{}:
		default:
		}
	}
	return nil
}

// Invalidate revokes pending work without allowing the same revision to rearm.
func (s *Service) Invalidate() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.invalidateLocked()
}

func (s *Service) run() {
	defer close(s.done)
	for range s.wake {
		for {
			s.mu.Lock()
			if s.closed {
				s.mu.Unlock()
				return
			}
			if s.pending == nil {
				s.mu.Unlock()
				break
			}
			if !time.Now().Before(s.pending.Deadline) {
				s.pending = nil
				s.mu.Unlock()
				continue
			}
			o := clone(*s.pending)
			s.pending = nil
			generation := s.generation
			ctx, cancel := context.WithDeadline(context.Background(), o.Deadline)
			s.cancel = cancel
			s.mu.Unlock()
			id, err := s.policy.Choose(ctx, o)
			cancel()
			s.mu.Lock()
			s.cancel = nil
			if err == nil && s.active && !s.consumed && generation == s.generation && time.Now().Before(o.Deadline) && slices.Contains(s.last.Candidates, id) {
				s.result = id
			}
			s.mu.Unlock()
		}
	}
}

// Poll returns only a completed policy proposal; it never starts fallback.
// ErrNoDecision includes a pending, rejected, stale or already consumed proposal.
// validate must check legality against the current confirmed observation, be
// bounded, and be safe to call without the service lock. Poll does not wait for
// the policy, but it does synchronously call validate when a proposal is ready.
func (s *Service) Poll(o Observation, validate func(string) bool) (string, error) {
	return s.resolve(o, validate, false)
}

// ResolveFallback chooses the first valid candidate in supplied priority order.
// It shares one consumption with Poll and cancels outstanding policy work after
// success. Call it before the deadline, leaving enough time for execution.
// The caller must still check freshness and prevent duplicate delivery.
func (s *Service) ResolveFallback(o Observation, validate func(string) bool) (string, error) {
	return s.resolve(o, validate, true)
}

func (s *Service) resolve(o Observation, validate func(string) bool, fallback bool) (string, error) {
	if validate == nil {
		return "", ErrNoDecision
	}
	s.mu.Lock()
	if s.closed || !s.active || s.consumed || s.last == nil || !same(*s.last, o) || !time.Now().Before(o.Deadline) {
		s.mu.Unlock()
		return "", ErrNoDecision
	}
	generation := s.generation
	var candidates []string
	if fallback {
		candidates = slices.Clone(s.last.Candidates)
	} else if s.result != "" {
		candidates = []string{s.result}
	}
	s.mu.Unlock()
	for _, id := range candidates {
		if !time.Now().Before(o.Deadline) {
			break
		}
		if !validate(id) {
			continue
		}
		s.mu.Lock()
		if s.closed || !s.active || s.consumed || s.generation != generation || !time.Now().Before(o.Deadline) {
			s.mu.Unlock()
			return "", ErrNoDecision
		}
		s.consumed = true
		s.pending = nil
		s.result = ""
		if s.cancel != nil {
			s.cancel()
		}
		s.mu.Unlock()
		return id, nil
	}
	return "", ErrNoDecision
}

// Close cancels and joins the worker. Policy implementations must honor context.
func (s *Service) Close() {
	s.mu.Lock()
	s.closed = true
	s.invalidateLocked()
	select {
	case s.wake <- struct{}{}:
	default:
	}
	s.mu.Unlock()
	<-s.done
}
