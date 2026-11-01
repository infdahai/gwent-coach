//go:build windows

package observe

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func trainingFrame(path string, model *PhaseModel) (navFrame, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return navFrame{}, err
	}
	var r report
	if err = json.Unmarshal(b, &r); err != nil {
		return navFrame{}, err
	}
	img, err := readImage(r.Screenshot)
	if err != nil {
		return navFrame{}, err
	}
	phase, scores := model.Recognize(img)
	at, err := time.Parse(time.RFC3339Nano, r.CapturedAt)
	if err != nil {
		return navFrame{}, err
	}
	frame := navFrame{Phase: phase, Reference: path, Captured: at, Scores: scores}
	for _, w := range r.Windows {
		if w.Handle == r.Foreground {
			frame.Width = int(w.Client.Right - w.Client.Left)
			frame.Height = int(w.Client.Bottom - w.Client.Top)
		}
	}
	return frame, nil
}

// RunTraining runs in the interactive Windows session. Model identity is pinned;
// model calibration remains the operator's responsibility.
func RunTraining(out string, logger *log.Logger, t Training) error {
	model, err := loadBoundPhases(t.ModelPath, t.ModelSHA256)
	if err != nil {
		return err
	}
	if !strings.EqualFold(filepath.Clean(t.StopFile), `D:\gwent-coach-training-local\PAUSE`) {
		return fmt.Errorf("training stop file must be D:\\gwent-coach-training-local\\PAUSE")
	}
	if err := acquireInterlock(`D:\gwent-coach-training-local\training-autoplay-interlock`); err != nil {
		return err
	}
	capture := func() (navFrame, error) {
		dir, err := os.MkdirTemp(out, "training-frame-")
		if err != nil {
			return navFrame{}, err
		}
		// Use the same native pixels as Execute's semantic guard. JPEG and
		// resizing can otherwise change the classification at the threshold.
		if err = runSample(dir, logger, Sampling{}); err != nil {
			return navFrame{}, err
		}
		paths, err := filepath.Glob(filepath.Join(dir, "*", "report.json"))
		if err != nil || len(paths) != 1 {
			return navFrame{}, fmt.Errorf("missing training report")
		}
		return trainingFrame(paths[0], model)
	}
	send := func(frame navFrame, action trainingAction) error {
		if frame.Width != 1920 || frame.Height != 1080 {
			return fmt.Errorf("unvalidated client geometry")
		}
		return Execute(out, logger, Action{Kind: action.kind, X: action.x, Y: action.y, Reference: frame.Reference, StopFile: t.StopFile, PhaseModel: t.ModelPath, ExpectedPhase: frame.Phase, phaseModel: model})
	}
	return runTraining(t, capture, send, func() error { return stopped(t.StopFile) }, func() { time.Sleep(500 * time.Millisecond) })
}
