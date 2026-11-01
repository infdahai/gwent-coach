// Package stateestimate combines visual evidence without controlling the game.
// UI stage labels and public game facts are separate: a stage never supplies a View.
package stateestimate

import (
	"image"
	"time"

	"gwentcoachmvp/internal/rules"
)

// FrameRef identifies one capture. IDs must identify actual captures, not reads
// of the same file. The capture owner establishes match and player identity.
type FrameRef struct {
	ID, MatchID   string
	CapturedAt    time.Time
	Width, Height int
	Player        int
}

type RoundFacts struct {
	Round int
	Wins  [2]int
}

type TurnFacts struct {
	Player        int
	Passed        [2]bool
	BetweenRounds bool
}

type TotalsFacts struct {
	Score, HandCount, DeckCount [2]int
}

// CardsFacts describes the entire visible public set and local hand order.
// CardID zero is explicitly unresolved; it cannot produce a complete View.
type CardsFacts struct {
	Visible   []rules.Instance
	HandOrder []string
}

type OutcomeFacts struct {
	Finished bool
	Winner   int // -1 for ongoing or draw; 0/1 only for a finished match
}

// Facts uses nil for unknown. In particular, zero counts, false flags and empty
// card sets are known only when their enclosing pointer is non-nil.
type Facts struct {
	Round   *RoundFacts
	Turn    *TurnFacts
	Totals  *TotalsFacts
	Cards   *CardsFacts
	Outcome *OutcomeFacts
}

// FieldEvidence identifies the captures supporting an agreed field. A zero value
// means unknown; timestamps belong to the captures, not the time of estimation.
type FieldEvidence struct {
	Known         bool
	Frames        [2]FrameRef
	CalibrationID string
}

// Target identifies an observed actionable object, independently of game facts.
// Kind is control, hand-instance, row, or board-target; ID identifies the object.
// Bounds are in native frame coordinates and LayoutID identifies the observed layout.
type Target struct {
	Kind, ID, LayoutID string
	Bounds             image.Rectangle
}

type TargetObservation struct {
	Target   Target
	Evidence FieldEvidence
}

// Evidence is the output of a recognizer for one frame. CalibrationID identifies
// the immutable detector configuration. Stage is a UI label (or "unknown"),
// never a lookup key for Facts. Predictions and input receipts are not Evidence.
type Evidence struct {
	Frame         FrameRef
	CalibrationID string
	Stage         string
	Facts         Facts
	Scalars       map[string]int
	Targets       []Target
}

// Task identifies the evidence needed for a particular observation use. Readiness
// does not authorize input or establish that a preceding action caused a result.
type Task string

const (
	TaskResult Task = "result"
)

// ReadyFor reports whether this observation supports the requested task. A
// result requires independently observed terminal outcome facts; a stage label
// alone never implies a result. Unknown tasks are not ready.
func (o Observation) ReadyFor(task Task) bool {
	switch task {
	case TaskResult:
		return o.Facts.Outcome != nil && o.Facts.Outcome.Finished && o.Facts.Outcome.Winner >= -1 && o.Facts.Outcome.Winner <= 1
	default:
		return false
	}
}

// Observation retains agreed partial facts. View is nil until all required
// facts agree and their public-state invariants hold. Eligible is always false
// without a complete View. StateKey excludes capture IDs and timestamps.
type Observation struct {
	Frame                          FrameRef
	Sources                        [2]string
	CalibrationID, Stage, StateKey string
	Facts                          Facts
	FieldEvidence                  map[string]FieldEvidence
	Scalars                        map[string]int
	Targets                        []TargetObservation
	View                           *rules.View
	Eligible                       bool
	Missing                        []string
}
