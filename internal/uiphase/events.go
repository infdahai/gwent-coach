package uiphase

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	"time"
)

// FrameRef identifies a real capture in an opaque stream. A stream can span
// menus and task screens; no match, player or game-state concept is required.
type FrameRef struct {
	ID, StreamID  string
	CapturedAt    time.Time
	Width, Height int
}

// Frame pixels must remain immutable for the duration of Observe.
type Frame struct {
	Ref   FrameRef
	Image image.Image
}

// Evidence is one classification, not a confirmed stage or an input permit.
// ModelID is a fingerprint of canonical model JSON, not the model file's hash.
type Evidence struct {
	Frame               FrameRef
	ModelID             string
	Phase               string
	Scores              []PhaseScore
	RecognitionDuration time.Duration
}

// Event reports the effective stage after a frame. Phase is unknown until two
// consecutive distinct fresh frames agree. Evidence always contains the raw
// single-frame classification. Confirmed sources are ordered oldest first.
// Changed describes effective-stage changes, including loss to unknown; it
// never asserts that an input or a game action caused the change.
type Event struct {
	Evidence           Evidence
	Previous, Phase    string
	Confirmed, Changed bool
	Sources            [2]FrameRef
}

// Recognizer owns one pinned model and one ordered stream. Observe calls must
// be serialized. Create a new recognizer to change streams or models.
type Recognizer struct {
	model                     PhaseModel
	modelID, streamID         string
	maxAge                    time.Duration
	last, candidate           FrameRef
	candidatePhase, effective string
}

func Fingerprint(model PhaseModel) (string, error) {
	if err := model.Validate(); err != nil {
		return "", err
	}
	data, err := json.Marshal(model)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func NewRecognizer(model PhaseModel, streamID string, maxAge time.Duration) (*Recognizer, error) {
	if streamID == "" || maxAge <= 0 {
		return nil, fmt.Errorf("stream ID and positive maximum frame age required")
	}
	id, err := Fingerprint(model)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(model)
	if err != nil {
		return nil, err
	}
	r := &Recognizer{modelID: id, streamID: streamID, maxAge: maxAge, effective: "unknown"}
	if err := json.Unmarshal(data, &r.model); err != nil {
		return nil, err
	}
	return r, nil
}

// Observe uses an explicit clock for deterministic offline replay. Invalid,
// reused, out-of-order, stale or foreign frames return no event and break
// continuity. A subsequent good frame cannot confirm an earlier candidate.
// The last valid capture time is retained to prevent replay after an error.
func (r *Recognizer) Observe(frame Frame, now time.Time) (Event, error) {
	ref := frame.Ref
	age := now.Sub(ref.CapturedAt)
	if now.IsZero() || ref.ID == "" || ref.StreamID != r.streamID || ref.CapturedAt.IsZero() || age < 0 || age > r.maxAge || ref.Width != r.model.Width || ref.Height != r.model.Height || frame.Image == nil || frame.Image.Bounds().Dx() != ref.Width || frame.Image.Bounds().Dy() != ref.Height || (r.last.ID != "" && (ref.ID == r.last.ID || !ref.CapturedAt.After(r.last.CapturedAt))) {
		r.candidate, r.candidatePhase, r.effective = FrameRef{}, "", "unknown"
		return Event{}, fmt.Errorf("fresh, ordered, distinct native capture in the pinned stream required")
	}
	started := time.Now()
	phase, scores := r.model.Recognize(frame.Image)
	event := Event{Evidence: Evidence{Frame: ref, ModelID: r.modelID, Phase: phase, Scores: scores, RecognitionDuration: time.Since(started)}, Previous: r.effective, Phase: "unknown"}
	if phase != "unknown" && phase == r.candidatePhase && r.candidate.ID != "" && now.Sub(r.candidate.CapturedAt) <= r.maxAge {
		event.Phase, event.Confirmed, event.Sources = phase, true, [2]FrameRef{r.candidate, ref}
	}
	if phase == "unknown" {
		r.candidate, r.candidatePhase = FrameRef{}, ""
	} else {
		r.candidate, r.candidatePhase = ref, phase
	}
	event.Changed = event.Previous != event.Phase
	r.last, r.effective = ref, event.Phase
	return event, nil
}
