package observe

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestInterlockConcurrentAndPersistent(t *testing.T) {
	root := t.TempDir()
	var wins atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if acquireInterlock(root) == nil {
				wins.Add(1)
			}
		}()
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatalf("claims=%d", wins.Load())
	}
	if acquireInterlock(root) == nil {
		t.Fatal("restart bypassed claim")
	}
}
func TestModelDigestBinding(t *testing.T) {
	a := Anchor{X: 0, Y: 0, Width: 16, Height: 8, MaxError: .01, Pixels: make([]float64, 16*8*3)}
	b, e := json.Marshal(PhaseModel{Width: 32, Height: 16, Phases: []Phase{{Name: "training", Anchors: []Anchor{a, a}}}})
	if e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(t.TempDir(), "model.json")
	if e = os.WriteFile(path, b, 0600); e != nil {
		t.Fatal(e)
	}
	h := sha256.Sum256(b)
	digest := hex.EncodeToString(h[:])
	m, e := loadBoundPhases(path, digest)
	if e != nil || m == nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(path, append(b, ' '), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = loadBoundPhases(path, digest); e == nil {
		t.Fatal("changed model accepted")
	}
}
func TestUnresolvedBlocksDifferentSession(t *testing.T) {
	root := t.TempDir()
	first := Navigation{StateDir: t.TempDir(), InterlockDir: root, StopFile: "pause", Steps: 1, plan: NavigationPlan{Transitions: []Transition{{From: "main", To: "training"}}}}
	sends, frames := 0, 0
	send := func(navFrame, Transition) error { sends++; return os.ErrDeadlineExceeded }
	obs := func() (navFrame, error) {
		frames++
		return navFrame{Phase: "main", Reference: fmt.Sprint(frames), Captured: time.Now()}, nil
	}
	if runNavigation(first, obs, send, func() error { return nil }) == nil || sends != 1 {
		t.Fatalf("expected one uncertain delivery, sends=%d", sends)
	}
	first.StateDir = t.TempDir()
	calls := 0
	if runNavigation(first, obs, func(navFrame, Transition) error { calls++; return nil }, func() error { return nil }) == nil || calls != 0 {
		t.Fatal("different session bypassed interlock")
	}
}
