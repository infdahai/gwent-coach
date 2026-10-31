package observe

import "fmt"

// Action is a single operator-reviewed input tied to a recent observation.
type Action struct {
	Kind      string `json:"kind"`
	X         int    `json:"x"`
	Y         int    `json:"y"`
	Reference string `json:"reference"`
	StopFile  string `json:"stop_file"`
}

func (a Action) Validate() error {
	if a.Kind != "click" && a.Kind != "hold" && a.Kind != "escape" {
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
