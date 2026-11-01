//go:build windows

package observe

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"gwentcoachmvp/internal/uiphase"
)

func newNativeCapture(out string, logger *log.Logger, cfg NativeLoopConfig, model *PhaseModel) (func(context.Context) (loopFrame, error), error) {
	recognizer, err := uiphase.NewRecognizer(*model, cfg.MatchID, 2*time.Second)
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context) (loopFrame, error) {
		if err := ctx.Err(); err != nil {
			return loopFrame{}, err
		}
		if err := stopped(cfg.StopFile); err != nil {
			return loopFrame{}, err
		}
		dir, err := os.MkdirTemp(out, "native-loop-frame-")
		if err != nil {
			return loopFrame{}, err
		}
		if err = runSample(dir, logger, Sampling{JPEGQuality: 85}); err != nil {
			return loopFrame{}, err
		}
		paths, err := filepath.Glob(filepath.Join(dir, "*", "report.json"))
		if err != nil || len(paths) != 1 {
			return loopFrame{}, fmt.Errorf("missing native loop report")
		}
		b, err := os.ReadFile(paths[0])
		if err != nil {
			return loopFrame{}, err
		}
		var r report
		if err = json.Unmarshal(b, &r); err != nil {
			return loopFrame{}, err
		}
		img, err := readImage(r.Screenshot)
		if err != nil {
			return loopFrame{}, err
		}
		at, err := time.Parse(time.RFC3339Nano, r.CapturedAt)
		if err != nil {
			return loopFrame{}, err
		}
		nav := navFrame{Reference: paths[0], Captured: at}
		for _, w := range r.Windows {
			if w.Handle == r.Foreground {
				nav.Width = int(w.Client.Right - w.Client.Left)
				nav.Height = int(w.Client.Bottom - w.Client.Top)
			}
		}
		if nav.Width != 1920 || nav.Height != 1080 {
			return loopFrame{}, fmt.Errorf("unvalidated native geometry")
		}
		stage, err := recognizer.Observe(uiphase.Frame{Ref: uiphase.FrameRef{ID: paths[0], StreamID: cfg.MatchID, CapturedAt: at, Width: nav.Width, Height: nav.Height}, Image: img}, time.Now())
		if err != nil {
			return loopFrame{}, err
		}
		nav.Phase, nav.Scores = stage.Evidence.Phase, stage.Evidence.Scores
		return loopFrame{navFrame: nav, Image: img, Stage: &stage}, nil
	}, nil
}
