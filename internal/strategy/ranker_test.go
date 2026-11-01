package strategy

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestExecutablePolicySelectionAndRanking(t *testing.T) {
	c := Compatibility{RulesVersion: "r", CatalogVersion: "c"}
	versions, err := NewExecutablePolicies("baseline", nil, c)
	if err != nil {
		t.Fatal(err)
	}
	baseline := versions.BeginMatch()
	encode := func(payload string) []byte {
		t.Helper()
		data, err := MarshalArtifact(Artifact{SchemaVersion: 1, PolicyVersion: "v2", RulesVersion: "r", CatalogVersion: "c", Payload: []byte(payload)})
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	if err := versions.Select(encode(`{"kind":"action-ranker-v1","card_weights":{"b":10},"kind_weights":{"play":1}}`)); err != nil {
		t.Fatal(err)
	}
	selected := versions.BeginMatch()
	if baseline.Version != "baseline" || baseline.Policy != nil || selected.Version != "v2" {
		t.Fatal("match snapshot not pinned")
	}
	ids := []string{`{"Kind":"play","Card":"a"}`, `{"Kind":"play","Card":"b"}`, `{"Kind":"pass"}`}
	o := Observation{MatchID: "m", PolicyVersion: selected.Version, Revision: 1, Deadline: time.Now().Add(time.Second), Candidates: ids}
	id, err := selected.Policy.Choose(context.Background(), o)
	if err != nil || id != ids[1] {
		t.Fatalf("ranker got %q: %v", id, err)
	}
	s := NewVersioned(selected.Policy, selected.Version)
	defer s.Close()
	if err := s.Start(o); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		got, err := s.Poll(o, func(id string) bool { return id == ids[1] })
		if err == nil {
			if got != ids[1] {
				t.Fatalf("unexpected %q", got)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("proposal unavailable: %v", err)
		}
		time.Sleep(time.Millisecond)
	}
	for _, payload := range []string{`{"kind":"offline-statistics-v1"}`, `{"kind":"action-ranker-v1","extra":1}`, `{"kind":"action-ranker-v1","kind_weights":{"bad":1}}`} {
		if err := versions.Select(encode(payload)); err == nil {
			t.Fatalf("accepted %s", payload)
		}
		if versions.BeginMatch().Version != "v2" {
			t.Fatal("rejected candidate replaced policy")
		}
	}
}

func TestRankerBaselineTieAndCancellation(t *testing.T) {
	p, err := NewRanker([]byte(`{"kind":"action-ranker-v1"}`))
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{`{"Kind":"play","Card":"a"}`, `{"Kind":"play","Card":"b"}`}
	got, err := p.Choose(context.Background(), Observation{Candidates: ids})
	if err != nil || got != ids[0] {
		t.Fatalf("tie: %q %v", got, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.Choose(ctx, Observation{Candidates: ids}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := p.Choose(context.Background(), Observation{Candidates: []string{"bad"}}); err == nil {
		t.Fatal("accepted malformed candidate")
	}
}
