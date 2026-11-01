//go:build windows

package observe

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"
)

func navigatePlatform(out string, logger *log.Logger, n Navigation, m *PhaseModel) error {
	// The launcher requires a single durable state directory shared by every run.
	// Keep the claim after every exit until an operator reconciles the ledger.
	if filepath.Clean(n.StateDir) != filepath.Clean(filepath.Join(filepath.Dir(n.StopFile), "training-navigation-state")) {
		return fmt.Errorf("navigation state must be the fixed training-navigation-state directory beside the stop file")
	}
	return navigationPrototype(out, logger, n, m)
}

func navigationPrototype(out string, logger *log.Logger, n Navigation, m *PhaseModel) error {
	// Interlock location is fixed per interactive account, independent of output,
	// stop-file and caller-chosen session directories.
	root, err := os.UserConfigDir()
	if err != nil {
		return err
	}
	n.InterlockDir = filepath.Join(root, "GwentCoach", "training-navigation")
	capture := func() (navFrame, error) {
		dir, e := os.MkdirTemp(out, "nav-frame-")
		if e != nil {
			return navFrame{}, e
		}
		if e = runSample(dir, logger, Sampling{}); e != nil {
			return navFrame{}, e
		}
		paths, e := filepath.Glob(filepath.Join(dir, "*", "report.json"))
		if e != nil || len(paths) != 1 {
			return navFrame{}, fmt.Errorf("missing navigation report")
		}
		b, e := os.ReadFile(paths[0])
		if e != nil {
			return navFrame{}, e
		}
		var r report
		if e = json.Unmarshal(b, &r); e != nil {
			return navFrame{}, e
		}
		img, e := readImage(r.Screenshot)
		if e != nil {
			return navFrame{}, e
		}
		phase, scores := m.Recognize(img)
		at, e := time.Parse(time.RFC3339Nano, r.CapturedAt)
		if e != nil {
			return navFrame{}, e
		}
		v := navFrame{Phase: phase, Reference: paths[0], Captured: at, Scores: scores}
		for _, w := range r.Windows {
			if w.Handle == r.Foreground {
				v.Width = int(w.Client.Right - w.Client.Left)
				v.Height = int(w.Client.Bottom - w.Client.Top)
			}
		}
		return v, nil
	}
	send := func(v navFrame, t Transition) error {
		if v.Width != 1920 || v.Height != 1080 {
			return fmt.Errorf("unvalidated native geometry")
		}
		return Execute(out, logger, Action{Kind: "click", X: t.X, Y: t.Y, Reference: v.Reference, StopFile: n.StopFile, PhaseModel: n.ModelPath, ExpectedPhase: t.From, phaseModel: m})
	}
	return runNavigation(n, capture, send, func() error { return stopped(n.StopFile) })
}
