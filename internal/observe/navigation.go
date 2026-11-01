package observe

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Navigation deliberately has no match-start or GG action.
type Transition struct {
	From, To string
	X, Y     int
	// Target is an operator-labelled visible target crop in native coordinates.
	Target image.Rectangle
}

// NavigationPlan binds every click to RGB evidence for its target in every
// variant of the source page. Labels and target semantics require calibration.
type NavigationPlan struct {
	Transitions []Transition
}

func (p NavigationPlan) validate(m *PhaseModel) error {
	if m.Width != 1920 || m.Height != 1080 || len(p.Transitions) == 0 || len(p.Transitions) > 8 {
		return fmt.Errorf("navigation requires native geometry and 1..8 transitions")
	}
	names := map[string]bool{}
	for _, phase := range m.Phases {
		names[phase.Name] = true
	}
	seen := map[string]bool{}
	for _, t := range p.Transitions {
		if !names[t.From] || !names[t.To] || t.From == t.To || t.From == "training" || seen[t.From] || t.Target.Empty() || !t.Target.In(image.Rect(0, 0, m.Width, m.Height)) || !image.Pt(t.X, t.Y).In(t.Target) || !m.RequiresRGBAnchor(t.From, t.Target, .15) {
			return fmt.Errorf("transition %q requires a unique calibrated page and target", t.From)
		}
		seen[t.From] = true
	}
	for _, start := range p.Transitions {
		current := start.From
		visited := map[string]bool{}
		for current != "training" {
			if visited[current] {
				return fmt.Errorf("navigation route contains a cycle")
			}
			visited[current] = true
			next := ""
			for _, t := range p.Transitions {
				if t.From == current {
					next = t.To
					break
				}
			}
			if next == "" {
				return fmt.Errorf("navigation route does not reach training")
			}
			current = next
		}
	}
	return nil
}

type Navigation struct {
	ModelPath, ModelSHA256, PlanPath, PlanSHA256, StopFile, StateDir, InterlockDir string
	Steps                                                                          int
	plan                                                                           NavigationPlan
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
		if a.Reference == "" || a.Reference == b.Reference || !b.Captured.After(a.Captured) || time.Since(a.Captured) > 2*time.Second || a.Phase == "unknown" || a.Phase != b.Phase {
			return fmt.Errorf("unknown or unstable navigation phase")
		}
		if b.Phase == "training" {
			return record("training_entry_verified_no_match_started", b, nil, nil)
		}
		var t Transition
		for _, candidate := range n.plan.Transitions {
			if candidate.From == b.Phase {
				t = candidate
				break
			}
		}
		if t.From == "" {
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
		delivered := time.Now()
		consecutive := 0
		previous := ""
		var previousFrame navFrame
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
			if v.Phase == t.To && v.Reference != "" && v.Captured.After(delivered) {
				if previous != v.Phase || v.Reference == previousFrame.Reference || !v.Captured.After(previousFrame.Captured) || time.Since(previousFrame.Captured) > 2*time.Second {
					consecutive = 0
				}
				previous = v.Phase
				previousFrame = v
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
	data, e := os.ReadFile(n.PlanPath)
	if e != nil {
		return e
	}
	sum := sha256.Sum256(data)
	if len(n.PlanSHA256) != 64 || !strings.EqualFold(n.PlanSHA256, hex.EncodeToString(sum[:])) {
		return fmt.Errorf("navigation plan hash mismatch")
	}
	if e = json.Unmarshal(data, &n.plan); e != nil {
		return e
	}
	if e = n.plan.validate(model); e != nil {
		return e
	}
	return navigatePlatform(out, logger, n, model)
}
