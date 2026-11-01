package observe

import (
	"encoding/json"
	"sync"
	"testing"
)

func TestLifecycle(t *testing.T) {
	var l Lifecycle
	s, changed, err := l.Publish("frame-1", "board", json.RawMessage(`{"hand":10}`))
	if err != nil || !changed || s.Version != 1 {
		t.Fatalf("%+v %v %v", s, changed, err)
	}
	s.State[0] = 'x'
	s, changed, err = l.Publish("frame-2", "board", json.RawMessage(`{"hand":10}`))
	if err != nil || changed || s.Version != 1 || s.Source != "frame-2" {
		t.Fatalf("%+v %v %v", s, changed, err)
	}
	intent := Intent{ID: "a", Version: 1, Action: json.RawMessage(`{"kind":"play"}`)}
	if _, err = l.Begin(intent); err != nil {
		t.Fatal(err)
	}
	if _, err = l.Begin(Intent{ID: "b", Version: 1, Action: json.RawMessage(`{}`)}); err == nil {
		t.Fatal("accepted concurrent action")
	}
	if err = l.Confirm("a", 1, true); err == nil {
		t.Fatal("confirmed without new state")
	}
	s, changed, err = l.Publish("frame-3", "board", json.RawMessage(`{"hand":9}`))
	if err != nil || !changed || s.Version != 2 {
		t.Fatal(s, changed, err)
	}
	if err = l.Confirm("a", 2, false); err == nil {
		t.Fatal("accepted unverified outcome")
	}
	if err = l.Confirm("a", 2, true); err != nil {
		t.Fatal(err)
	}
	if _, err = l.Begin(intent); err == nil {
		t.Fatal("accepted duplicate action")
	}
	if _, err = l.Begin(Intent{ID: "c", Version: 1, Action: json.RawMessage(`{}`)}); err == nil {
		t.Fatal("accepted stale version")
	}
}

func TestLifecycleConcurrentBegin(t *testing.T) {
	var l Lifecycle
	if _, _, err := l.Publish("frame", "board", json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, id := range []string{"a", "b"} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			_, err := l.Begin(Intent{ID: id, Version: 1, Action: json.RawMessage(`{}`)})
			results <- err
		}(id)
	}
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatalf("accepted %d actions", success)
	}
}
