package observe

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"

	"gwentcoachmvp/internal/stateestimate"
	"gwentcoachmvp/internal/uiphase"
)

// RGBRequirement declares a region that must constrain every alternative for a
// fact. Geometry alone does not prove the labels; calibration remains explicit.
type RGBRequirement struct {
	ROI      image.Rectangle
	MaxError float64
}

// FactCalibration recognizes labels independently from the UI stage model.
// Each label names a field-group value, never a full rules.View. At least one
// required RGB region must appear in every variant of every label.
type FactCalibration[T any] struct {
	Model       PhaseModel
	Values      map[string]T
	RequiredRGB []RGBRequirement
}

type EvidenceCalibration struct {
	Stage   PhaseModel
	Round   *FactCalibration[stateestimate.RoundFacts]
	Turn    *FactCalibration[stateestimate.TurnFacts]
	Totals  *FactCalibration[stateestimate.TotalsFacts]
	Cards   *FactCalibration[stateestimate.CardsFacts]
	Outcome *FactCalibration[stateestimate.OutcomeFacts]
	Targets []*FactCalibration[stateestimate.Target]
	// Scalars are independently labeled numeric regions (for example score or
	// round counters), not OCR or implicit parts of a complete game view.
	Scalars map[string]*FactCalibration[int]
	Digits  map[string]DigitCalibration
}

// CalibratedEvidenceReader is an immutable, restricted labelled-template
// recognizer. Missing, unknown or ambiguous fact models produce nil facts.
// It does not estimate state, infer events from clicks, or send any input.
type CalibratedEvidenceReader struct {
	cal         EvidenceCalibration
	id, stageID string
}

func NewCalibratedEvidenceReader(cal EvidenceCalibration) (*CalibratedEvidenceReader, error) {
	if err := cal.Stage.Validate(); err != nil {
		return nil, fmt.Errorf("stage calibration: %w", err)
	}
	w, h := cal.Stage.Width, cal.Stage.Height
	for _, err := range []error{validateFactCalibration(cal.Round, w, h), validateFactCalibration(cal.Turn, w, h), validateFactCalibration(cal.Totals, w, h), validateFactCalibration(cal.Cards, w, h), validateFactCalibration(cal.Outcome, w, h)} {
		if err != nil {
			return nil, err
		}
	}
	for _, target := range cal.Targets {
		if target == nil {
			return nil, fmt.Errorf("nil target calibration")
		}
		if err := validateFactCalibration(target, w, h); err != nil {
			return nil, err
		}
		for _, value := range target.Values {
			if (value.Kind != "control" && value.Kind != "hand-instance" && value.Kind != "row" && value.Kind != "board-target") || value.ID == "" || value.LayoutID == "" || value.Bounds.Empty() || !value.Bounds.In(image.Rect(0, 0, w, h)) {
				return nil, fmt.Errorf("invalid target calibration")
			}
		}
	}
	for name, scalar := range cal.Scalars {
		if name == "" || scalar == nil {
			return nil, fmt.Errorf("invalid scalar calibration")
		}
		if err := validateFactCalibration(scalar, w, h); err != nil {
			return nil, err
		}
	}
	for name, digits := range cal.Digits {
		if name == "" || cal.Scalars[name] != nil {
			return nil, fmt.Errorf("invalid or duplicate digit calibration")
		}
		if err := digits.validate(image.Rect(0, 0, w, h)); err != nil {
			return nil, fmt.Errorf("digits %q: %w", name, err)
		}
	}
	data, err := json.Marshal(cal)
	if err != nil {
		return nil, err
	}
	r := &CalibratedEvidenceReader{}
	if err = json.Unmarshal(data, &r.cal); err != nil {
		return nil, err
	}
	sum := sha256.Sum256(data)
	r.id = hex.EncodeToString(sum[:])
	r.stageID, err = uiphase.Fingerprint(r.cal.Stage)
	if err != nil {
		return nil, err
	}
	return r, nil
}

func validateFactCalibration[T any](cal *FactCalibration[T], w, h int) error {
	if cal == nil {
		return nil
	}
	if err := cal.Model.Validate(); err != nil {
		return err
	}
	if cal.Model.Width != w || cal.Model.Height != h || len(cal.RequiredRGB) == 0 || len(cal.Values) != len(cal.Model.Phases) {
		return fmt.Errorf("fact calibration requires native geometry, labelled values and RGB evidence")
	}
	for _, p := range cal.Model.Phases {
		if _, ok := cal.Values[p.Name]; !ok {
			return fmt.Errorf("fact label %q has no value", p.Name)
		}
		for _, r := range cal.RequiredRGB {
			if !cal.Model.RequiresRGBAnchor(p.Name, r.ROI, r.MaxError) {
				return fmt.Errorf("fact %q lacks required RGB evidence in a variant", p.Name)
			}
		}
	}
	return nil
}

func readFact[T any](cal *FactCalibration[T], img image.Image) *T {
	if cal == nil {
		return nil
	}
	name, _ := cal.Model.Recognize(img)
	v, ok := cal.Values[name]
	if !ok {
		return nil
	}
	return &v
}

// Read uses one immutable native frame for all detectors. It does no file IO.
// Freshness and agreement are checked by stateestimate.Estimate, with an
// explicit clock so exactly the same evidence can be replayed offline.
func (r *CalibratedEvidenceReader) Read(frame stateestimate.FrameRef, img image.Image) (stateestimate.Evidence, error) {
	if err := r.validateFrame(frame, img); err != nil {
		return stateestimate.Evidence{}, err
	}
	stage, _ := r.cal.Stage.Recognize(img)
	return r.readFacts(frame, img, stage), nil
}

// ReadWithStage consumes the single-frame evidence from a uiphase Event without
// running the stage model again. For this game adapter StreamID must equal
// MatchID. Evidence must refer to this exact frame and the pinned stage model.
// The event's confirmed stage is for task navigation; Estimate independently
// combines two raw stage observations with the two frames of game facts.
func (r *CalibratedEvidenceReader) ReadWithStage(frame stateestimate.FrameRef, img image.Image, stage uiphase.Evidence) (stateestimate.Evidence, error) {
	if err := r.validateFrame(frame, img); err != nil {
		return stateestimate.Evidence{}, err
	}
	if stage.ModelID != r.stageID || stage.Frame.ID != frame.ID || stage.Frame.StreamID != frame.MatchID || !stage.Frame.CapturedAt.Equal(frame.CapturedAt) || stage.Frame.Width != frame.Width || stage.Frame.Height != frame.Height {
		return stateestimate.Evidence{}, fmt.Errorf("stage evidence must match frame, stream and pinned model")
	}
	known := stage.Phase == "unknown"
	for _, p := range r.cal.Stage.Phases {
		known = known || stage.Phase == p.Name
	}
	if !known {
		return stateestimate.Evidence{}, fmt.Errorf("stage label absent from pinned model")
	}
	return r.readFacts(frame, img, stage.Phase), nil
}

func (r *CalibratedEvidenceReader) validateFrame(frame stateestimate.FrameRef, img image.Image) error {
	if img == nil || frame.ID == "" || frame.MatchID == "" || frame.CapturedAt.IsZero() || frame.Player < 0 || frame.Player > 1 || frame.Width != r.cal.Stage.Width || frame.Height != r.cal.Stage.Height || img.Bounds().Dx() != frame.Width || img.Bounds().Dy() != frame.Height {
		return fmt.Errorf("bound native frame required")
	}
	return nil
}

func (r *CalibratedEvidenceReader) readFacts(frame stateestimate.FrameRef, img image.Image, stage string) stateestimate.Evidence {
	facts := stateestimate.Facts{
		Round: readFact(r.cal.Round, img), Turn: readFact(r.cal.Turn, img), Totals: readFact(r.cal.Totals, img),
		Cards: readFact(r.cal.Cards, img), Outcome: readFact(r.cal.Outcome, img),
	}
	if facts.Cards != nil {
		facts.Cards.Visible = append(facts.Cards.Visible[:0:0], facts.Cards.Visible...)
		facts.Cards.HandOrder = append(facts.Cards.HandOrder[:0:0], facts.Cards.HandOrder...)
	}
	var targets []stateestimate.Target
	for _, cal := range r.cal.Targets {
		if target := readFact(cal, img); target != nil {
			targets = append(targets, *target)
		}
	}
	scalars := make(map[string]int)
	for name, cal := range r.cal.Scalars {
		if value := readFact(cal, img); value != nil {
			scalars[name] = *value
		}
	}
	for name, cal := range r.cal.Digits {
		if value, ok := cal.ReadDigits(img); ok {
			scalars[name] = value
		}
	}
	return stateestimate.Evidence{Frame: frame, CalibrationID: r.id, Stage: stage, Facts: facts, Targets: targets, Scalars: scalars}
}
