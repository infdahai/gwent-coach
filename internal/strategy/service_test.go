package strategy

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type policyFunc func(context.Context, Observation) (string, error)

func (f policyFunc) Choose(ctx context.Context, o Observation) (string, error) { return f(ctx, o) }

func observation() Observation {
	return Observation{MatchID: "match", Revision: 1, Deadline: time.Now().Add(time.Second), Candidates: []string{"first", "second"}}
}

func TestWorkerSkipsConsumedOrExpiredQueue(t *testing.T) {
	for _, mode := range []string{"consumed", "expired"} {
		t.Run(mode, func(t *testing.T) {
			started, release := make(chan struct{}), make(chan struct{})
			calls := make(chan uint64, 2)
			s := New(policyFunc(func(ctx context.Context, o Observation) (string, error) {
				calls <- o.Revision
				if o.Revision == 1 {
					close(started)
					<-release // Hold the worker while a newer observation queues.
				}
				return "second", nil
			}))
			defer s.Close()
			var once sync.Once
			defer once.Do(func() { close(release) })
			o := observation()
			if err := s.Start(o); err != nil {
				t.Fatal(err)
			}
			<-started
			next := clone(o)
			next.Revision++
			if mode == "expired" {
				next.Deadline = time.Now().Add(20 * time.Millisecond)
			}
			if err := s.Start(next); err != nil {
				t.Fatal(err)
			}
			if mode == "consumed" {
				if got, err := s.ResolveFallback(next, func(string) bool { return true }); err != nil || got != "first" {
					t.Fatalf("fallback: %q %v", got, err)
				}
			} else {
				<-time.After(time.Until(next.Deadline))
			}
			once.Do(func() { close(release) })
			until := time.Now().Add(time.Second)
			for {
				s.mu.Lock()
				idle := s.pending == nil && s.cancel == nil
				s.mu.Unlock()
				if idle {
					break
				}
				if time.Now().After(until) {
					t.Fatal("worker did not become idle")
				}
				time.Sleep(time.Millisecond)
			}
			if count := len(calls); count != 1 {
				t.Fatalf("policy called %d times; queued %s observation must not run", count, mode)
			}
			if _, err := s.Poll(next, func(string) bool { return true }); !errors.Is(err, ErrNoDecision) {
				t.Fatalf("obsolete result: %v", err)
			}
			latest := clone(o)
			latest.Revision = 3
			latest.Deadline = time.Now().Add(time.Second)
			if err := s.Start(latest); err != nil {
				t.Fatal(err)
			}
			for {
				got, err := s.Poll(latest, func(string) bool { return true })
				if err == nil {
					if got != "second" {
						t.Fatalf("latest proposal: %q", got)
					}
					break
				}
				if !errors.Is(err, ErrNoDecision) || time.Now().After(latest.Deadline) {
					t.Fatalf("worker failed to resume: %v", err)
				}
				time.Sleep(time.Millisecond)
			}
		})
	}
}

func TestFallbackConsumesRevision(t *testing.T) {
	started := make(chan struct{})
	finished := make(chan struct{})
	s := New(policyFunc(func(ctx context.Context, o Observation) (string, error) {
		close(started)
		<-ctx.Done()
		defer close(finished)
		return "second", nil
	}))
	defer s.Close()
	o := observation()
	if err := s.Start(o); err != nil {
		t.Fatal(err)
	}
	<-started
	if _, err := s.Poll(o, func(string) bool { return true }); !errors.Is(err, ErrNoDecision) {
		t.Fatalf("pending policy returned a fallback: %v", err)
	}
	got, err := s.ResolveFallback(o, func(id string) bool { return id == "second" })
	if err != nil || got != "second" {
		t.Fatalf("fallback: %q %v", got, err)
	}
	<-finished
	if err := s.Start(o); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Poll(o, func(string) bool { return true }); !errors.Is(err, ErrNoDecision) {
		t.Fatalf("late policy result: %v", err)
	}
	if _, err := s.ResolveFallback(o, func(string) bool { return true }); !errors.Is(err, ErrNoDecision) {
		t.Fatalf("duplicate: %v", err)
	}
}

func TestPolicyResultAndCandidateValidation(t *testing.T) {
	for _, tc := range []struct {
		name, choice, want string
		allowed            func(string) bool
	}{
		{"preferred", "second", "second", func(string) bool { return true }},
		{"foreign proposal", "foreign", "first", func(string) bool { return true }},
		{"recheck proposal", "second", "first", func(id string) bool { return id == "first" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := New(policyFunc(func(context.Context, Observation) (string, error) { return tc.choice, nil }))
			defer s.Close()
			o := observation()
			if err := s.Start(o); err != nil {
				t.Fatal(err)
			}
			// Wait for the worker to complete, including rejected foreign proposals.
			for {
				s.mu.Lock()
				completed := s.pending == nil && s.cancel == nil
				s.mu.Unlock()
				if completed {
					break
				}
				if time.Now().After(o.Deadline) {
					t.Fatal("worker timeout")
				}
				time.Sleep(time.Millisecond)
			}
			got, err := s.Poll(o, tc.allowed)
			if tc.choice != "second" || tc.name == "recheck proposal" {
				if !errors.Is(err, ErrNoDecision) {
					t.Fatalf("unexpected policy result %q %v", got, err)
				}
				got, err = s.ResolveFallback(o, tc.allowed)
			}
			if err != nil || got != tc.want {
				t.Fatalf("got %q %v, want %q", got, err, tc.want)
			}
			if _, err := s.ResolveFallback(o, func(string) bool { return true }); !errors.Is(err, ErrNoDecision) {
				t.Fatalf("second consumption: %v", err)
			}
		})
	}
}

func TestObservationCopiedFromCallerAndPolicy(t *testing.T) {
	finished := make(chan struct{})
	s := New(policyFunc(func(ctx context.Context, o Observation) (string, error) {
		o.State[0] = 'X'
		o.Candidates[0] = "foreign"
		close(finished)
		return "foreign", nil
	}))
	defer s.Close()
	o := observation()
	o.State = []byte("state")
	if err := s.Start(o); err != nil {
		t.Fatal(err)
	}
	<-finished
	if string(o.State) != "state" || o.Candidates[0] != "first" {
		t.Fatal("policy mutated caller observation")
	}
	changed := clone(o)
	changed.State[0] = 'Y'
	if _, err := s.ResolveFallback(changed, func(string) bool { return true }); !errors.Is(err, ErrNoDecision) {
		t.Fatal("changed state accepted")
	}
	if got, err := s.ResolveFallback(o, func(string) bool { return true }); err != nil || got != "first" {
		t.Fatalf("fallback after policy mutation: %q %v", got, err)
	}
}

func TestObservationVersionAndDeadline(t *testing.T) {
	s := New(nil)
	defer s.Close()
	o := observation()
	if err := s.Start(o); err != nil {
		t.Fatal(err)
	}
	changed := clone(o)
	changed.Candidates[0] = "other"
	if err := s.Start(changed); !errors.Is(err, ErrObservation) {
		t.Fatalf("conflict: %v", err)
	}
	if _, err := s.ResolveFallback(o, func(string) bool { return true }); !errors.Is(err, ErrNoDecision) {
		t.Fatal("conflict did not invalidate")
	}
	if err := s.Start(o); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ResolveFallback(o, func(string) bool { return true }); !errors.Is(err, ErrNoDecision) {
		t.Fatal("same revision rearmed")
	}
	next := clone(o)
	next.Revision++
	if err := s.Start(next); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ResolveFallback(o, func(string) bool { return true }); !errors.Is(err, ErrNoDecision) {
		t.Fatal("stale observation accepted")
	}
	deadlineChanged := clone(next)
	deadlineChanged.Deadline = next.Deadline.Add(time.Second)
	if err := s.Start(deadlineChanged); !errors.Is(err, ErrObservation) {
		t.Fatal("changed deadline accepted")
	}
	expired := clone(next)
	expired.Revision++
	expired.Deadline = time.Now().Add(-time.Second)
	if err := s.Start(expired); !errors.Is(err, ErrObservation) {
		t.Fatal("expired input accepted")
	}
}

func TestResolveConcurrentAndInvalidation(t *testing.T) {
	s := New(nil)
	defer s.Close()
	o := observation()
	if err := s.Start(o); err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	result := make(chan error, 1)
	go func() {
		_, err := s.ResolveFallback(o, func(string) bool { close(entered); <-release; return true })
		result <- err
	}()
	<-entered
	s.Invalidate()
	close(release)
	if err := <-result; !errors.Is(err, ErrNoDecision) {
		t.Fatal("invalidated decision returned", err)
	}
	o.Revision++
	if err := s.Start(o); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.ResolveFallback(o, func(string) bool { return true })
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		} else if !errors.Is(err, ErrNoDecision) {
			t.Fatal(err)
		}
	}
	if success != 1 {
		t.Fatalf("got %d successful resolutions", success)
	}
}

func TestSupersededWorkerCannotPublish(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	newStarted := make(chan struct{})
	s := New(policyFunc(func(ctx context.Context, o Observation) (string, error) {
		if o.Revision == 1 {
			close(started)
			<-release // Simulate a policy that finishes after cancellation.
			return "second", nil
		}
		close(newStarted)
		return "second", nil
	}))
	defer s.Close()
	o := observation()
	if err := s.Start(o); err != nil {
		t.Fatal(err)
	}
	<-started
	next := clone(o)
	next.Revision++
	next.Candidates = []string{"first"}
	if err := s.Start(next); err != nil {
		t.Fatal(err)
	}
	close(release)
	<-newStarted
	got, err := s.ResolveFallback(next, func(string) bool { return true })
	if err != nil || got != "first" {
		t.Fatalf("superseded proposal: %q %v", got, err)
	}
	if _, err := s.Poll(next, func(string) bool { return true }); !errors.Is(err, ErrNoDecision) {
		t.Fatal("duplicate result", err)
	}
}

func TestPollAndFallbackConcurrent(t *testing.T) {
	s := New(policyFunc(func(context.Context, Observation) (string, error) { return "second", nil }))
	defer s.Close()
	o := observation()
	if err := s.Start(o); err != nil {
		t.Fatal(err)
	}
	for {
		s.mu.Lock()
		ready := s.result != ""
		s.mu.Unlock()
		if ready {
			break
		}
		if time.Now().After(o.Deadline) {
			t.Fatal("policy timeout")
		}
		time.Sleep(time.Millisecond)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	result := make(chan error, 1)
	go func() {
		_, err := s.Poll(o, func(string) bool { close(entered); <-release; return true })
		result <- err
	}()
	<-entered
	if got, err := s.ResolveFallback(o, func(string) bool { return true }); err != nil || got != "first" {
		t.Fatalf("fallback: %q %v", got, err)
	}
	close(release)
	if err := <-result; !errors.Is(err, ErrNoDecision) {
		t.Fatalf("poll consumed twice: %v", err)
	}
}

func TestPollDiscardedOnVersionChange(t *testing.T) {
	s := New(policyFunc(func(context.Context, Observation) (string, error) { return "second", nil }))
	defer s.Close()
	o := observation()
	if err := s.Start(o); err != nil {
		t.Fatal(err)
	}
	for {
		s.mu.Lock()
		ready := s.result != ""
		s.mu.Unlock()
		if ready {
			break
		}
		if time.Now().After(o.Deadline) {
			t.Fatal("policy timeout")
		}
		time.Sleep(time.Millisecond)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	result := make(chan error, 1)
	go func() {
		_, err := s.Poll(o, func(string) bool { close(entered); <-release; return true })
		result <- err
	}()
	<-entered
	next := clone(o)
	next.Revision++
	if err := s.Start(next); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-result; !errors.Is(err, ErrNoDecision) {
		t.Fatalf("stale poll: %v", err)
	}
}

func TestPollDeadlineDuringValidation(t *testing.T) {
	s := New(policyFunc(func(context.Context, Observation) (string, error) { return "second", nil }))
	defer s.Close()
	o := observation()
	o.Deadline = time.Now().Add(100 * time.Millisecond)
	if err := s.Start(o); err != nil {
		t.Fatal(err)
	}
	for {
		s.mu.Lock()
		ready := s.result != ""
		s.mu.Unlock()
		if ready {
			break
		}
		if time.Now().After(o.Deadline) {
			t.Fatal("policy timeout")
		}
		time.Sleep(time.Millisecond)
	}
	_, err := s.Poll(o, func(string) bool {
		time.Sleep(time.Until(o.Deadline) + time.Millisecond)
		return true
	})
	if !errors.Is(err, ErrNoDecision) {
		t.Fatalf("late policy decision: %v", err)
	}
}

func TestDeadlineDuringValidation(t *testing.T) {
	s := New(nil)
	defer s.Close()
	o := observation()
	o.Deadline = time.Now().Add(20 * time.Millisecond)
	if err := s.Start(o); err != nil {
		t.Fatal(err)
	}
	_, err := s.ResolveFallback(o, func(string) bool { time.Sleep(30 * time.Millisecond); return true })
	if !errors.Is(err, ErrNoDecision) {
		t.Fatalf("late decision: %v", err)
	}
}
