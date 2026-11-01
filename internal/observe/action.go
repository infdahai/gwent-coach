package observe

import (
	"fmt"
	"time"
)

// InputReceipt describes transport delivery, never semantic success.
// DeliveredAt is recorded at the input API boundary, before settling/capture.
type InputReceipt struct {
	Outcome     string    `json:"outcome"`
	EventsSent  uint32    `json:"events_sent"`
	DeliveredAt time.Time `json:"delivered_at,omitempty"`
}

// Action is a single operator-reviewed input tied to a recent observation.
type Action struct {
	Kind          string `json:"kind"`
	X             int    `json:"x"`
	Y             int    `json:"y"`
	Reference     string `json:"reference"`
	StopFile      string `json:"stop_file"`
	PhaseModel    string `json:"phase_model,omitempty"`
	ExpectedPhase string `json:"expected_phase,omitempty"`
	phaseModel    *PhaseModel
	deadline      time.Time
	guardProbe    bool
	primitiveOnly bool
	onReceipt     func(InputReceipt) error
}

func (a Action) Validate() error {
	if a.Kind != "click" && a.Kind != "hold" && a.Kind != "hover" && a.Kind != "escape" {
		return fmt.Errorf("unsupported action %q", a.Kind)
	}
	if a.Reference == "" || a.StopFile == "" {
		return fmt.Errorf("action requires an observation report and stop file path")
	}
	if a.X < 0 || a.Y < 0 {
		return fmt.Errorf("client coordinates must be nonnegative")
	}
	return nil
}
