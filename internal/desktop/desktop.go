// Package desktop provides foreground client capture and guarded manual input,
// independent of any game or autonomous session.
package desktop

import "time"

type Rect struct{ Left, Top, Right, Bottom int32 }

type Window struct {
	Handle    uintptr `json:"handle"`
	PID       uint32  `json:"pid"`
	Session   uint32  `json:"session"`
	Desktop   string  `json:"desktop"`
	DPI       uint32  `json:"dpi"`
	Client    Rect    `json:"client"`
	Minimized bool    `json:"minimized"`
}

type Capture struct {
	Window Window    `json:"window"`
	At     time.Time `json:"at"`
	PNG    []byte    `json:"png,omitempty"`
}

// Receipt describes OS input delivery, never application-level success.
type Receipt struct {
	Outcome    string `json:"outcome"`
	EventsSent uint32 `json:"events_sent"`
}

const MaxAge = 30 * time.Second
