package observe

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"
)

// Navigation deliberately has no match-start or GG action.
type Transition struct {
	From, To string
	X, Y     int
}

var trainingRoute = map[string]Transition{
	"result":  {"result", "rewards", 480, 514},
	"rewards": {"rewards", "modes", 446, 514},
	"main":    {"main", "modes", 435, 320},
	// Carousel coordinates vary by selection; no generic phase may authorize it.
	// A future validated route must identify the exact selected carousel state.

	"training-menu": {"training-menu", "training", 506, 288},
}

type Navigation struct {
	ModelPath, ModelSHA256, StopFile, StateDir, InterlockDir string
	Steps                                                    int
}
type navFrame struct {
	Phase, Reference string
	Captured         time.Time
	Width, Height    int
	Scores           []PhaseScore
}
type navEvent struct {
	Time, Status string
	Frame        navFrame
	Transition   *Transition `json:",omitempty"`
	Error        string      `json:",omitempty"`
}

// runNavigation keeps an exclusive session claim, including after failure. A new
// invocation cannot silently replay an uncertain action with a fresh screenshot.
func runNavigation(n Navigation, observe func() (navFrame, error), send func(navFrame, Transition) error, pause func() error) error {
	if n.StateDir == "" || n.StopFile == "" || n.Steps < 1 || n.Steps > 8 {
		return fmt.Errorf("navigation requires state directory, pause path and 1..8 steps")
	}
	if e := acquireInterlock(n.InterlockDir); e != nil {
		return e
	}
	if e := os.MkdirAll(n.StateDir, 0700); e != nil {
		return e
	}
	claim, e := os.OpenFile(filepath.Join(n.StateDir, "session.claim"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return fmt.Errorf("navigation session already claimed; review ledger before using a new state directory: %w", e)
	}
	if e = claim.Close(); e != nil {
		return e
	}
	f, e := os.OpenFile(filepath.Join(n.StateDir, "events.jsonl"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	defer f.Close()
	record := func(status string, frame navFrame, t *Transition, err error) error {
		ev := navEvent{Time: time.Now().UTC().Format(time.RFC3339Nano), Status: status, Frame: frame, Transition: t}
		if err != nil {
			ev.Error = err.Error()
		}
		if e := json.NewEncoder(f).Encode(ev); e != nil {
			return e
		}
		return f.Sync()
	}
	get := func() (navFrame, error) {
		if e := pause(); e != nil {
			return navFrame{}, e
		}
		v, e := observe()
		if e != nil {
			return v, e
		}
		age := time.Since(v.Captured)
		if age < 0 || age > 2*time.Second {
			return v, fmt.Errorf("stale navigation observation")
		}
		if e = record("observed", v, nil, nil); e != nil {
			return v, e
		}
		return v, nil
	}
	for step := 0; step < n.Steps; step++ {
		a, e := get()
		if e != nil {
			return e
		}
		b, e := get()
		if e != nil {
			return e
		}
		if a.Phase == "unknown" || a.Phase != b.Phase {
			return fmt.Errorf("unknown or unstable navigation phase")
		}
		if b.Phase == "training" {
			return record("training_entry_verified_no_match_started", b, nil, nil)
		}
		// Only the historical Bearly-selected carousel has a validated training tile location.
		// Refuse other carousel selections, including standard/online matchmaking.

		t, ok := trainingRoute[b.Phase]
		if !ok {
			return fmt.Errorf("phase %q has no authorized training transition", b.Phase)
		}
		if e = pause(); e != nil {
			return e
		}
		if e = record("intent_outcome_pending", b, &t, nil); e != nil {
			return e
		}
		if e = send(b, t); e != nil {
			_ = record("unresolved_do_not_replay", b, &t, e)
			return e
		}
		if e = record("delivered_unconfirmed", b, &t, nil); e != nil {
			return e
		}
		consecutive := 0
		previous := ""
		verified := false
		for attempt := 0; attempt < 12; attempt++ {
			v, e := get()
			if e != nil {
				_ = record("unresolved_do_not_replay", v, &t, e)
				return e
			}
			if v.Phase == "unknown" {
				e := fmt.Errorf("unknown postcondition; no retry")
				_ = record("unresolved_do_not_replay", v, &t, e)
				return e
			}
			if v.Phase == t.To || (t.From == "modes" && v.Phase == "training") {
				if previous != v.Phase {
					consecutive = 0
				}
				previous = v.Phase
				consecutive++
			} else {
				previous = ""
				consecutive = 0
			}
			if consecutive == 2 {
				if e = record("semantic_postcondition_verified", v, &t, nil); e != nil {
					return e
				}
				verified = true
				if v.Phase == "training" {
					return record("training_entry_verified_no_match_started", v, &t, nil)
				}
				break
			}
		}
		if !verified {
			e := fmt.Errorf("postcondition %s not confirmed; no retry", t.To)
			_ = record("unresolved_do_not_replay", b, &t, e)
			return e
		}
	}
	return fmt.Errorf("navigation step bound reached; inspect ledger")
}

func Navigate(out string, logger *log.Logger, n Navigation) error {
	model, e := loadBoundPhases(n.ModelPath, n.ModelSHA256)
	if e != nil {
		return e
	}
	return navigatePlatform(out, logger, n, model)
}
