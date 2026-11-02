//go:build windows

package observe

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"io"
	"log"
	"os"
	"path/filepath"
	"time"
)

func RunObservedMatch(ctx context.Context, out string, logger *log.Logger, cfg ObservedMatchConfig, output io.Writer) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if cfg.model == nil {
		return fmt.Errorf("load and validate observed match configuration first")
	}
	if err := stopped(cfg.StopFile); err != nil {
		return err
	}
	if err := os.Mkdir(cfg.StateDir, 0700); err != nil {
		return err
	}
	file, err := os.OpenFile(filepath.Join(cfg.StateDir, "events.jsonl"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer file.Close()
	journal := syncedSliceWriter{file: file, output: io.Discard}
	nativeLog, err := os.OpenFile(filepath.Join(cfg.StateDir, "native.log"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer nativeLog.Close()
	nativeLogger := log.New(nativeLog, "", log.LstdFlags|log.LUTC)
	logger.Printf("observed_match=%s state=%s config_sha256=%s", cfg.ID, cfg.StateDir, cfg.configSHA256)
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	binary, err := os.Open(exe)
	if err != nil {
		return err
	}
	digest := sha256.New()
	_, hashErr := io.Copy(digest, binary)
	if err = errors.Join(hashErr, binary.Close()); err != nil {
		return err
	}
	if err = json.NewEncoder(journal).Encode(struct {
		Status           string              `json:"status"`
		ConfigSHA256     string              `json:"config_sha256"`
		ExecutableSHA256 string              `json:"executable_sha256"`
		Config           ObservedMatchConfig `json:"config"`
	}{"attempt_created", cfg.configSHA256, hex.EncodeToString(digest.Sum(nil)), cfg}); err != nil {
		return err
	}
	// Share ownership with the legacy loop and navigation. Runtime never clears
	// this claim; changing StateDir cannot bypass an unresolved old input.
	if err = acquireInterlock(`D:\gwent-coach-training-local\training-autoplay-interlock`); err != nil {
		return err
	}
	capture := func(ctx context.Context) (observedMatchFrame, error) {
		if err := ctx.Err(); err != nil {
			return observedMatchFrame{}, err
		}
		if err := stopped(cfg.StopFile); err != nil {
			return observedMatchFrame{}, err
		}
		dir, err := os.MkdirTemp(out, "observed-match-frame-")
		if err != nil {
			return observedMatchFrame{}, err
		}
		if err = runSample(dir, nativeLogger, Sampling{}); err != nil {
			return observedMatchFrame{}, err
		}
		paths, err := filepath.Glob(filepath.Join(dir, "*", "report.json"))
		if err != nil || len(paths) != 1 {
			return observedMatchFrame{}, fmt.Errorf("missing observed match report")
		}
		data, err := os.ReadFile(paths[0])
		if err != nil {
			return observedMatchFrame{}, err
		}
		var r report
		if err = json.Unmarshal(data, &r); err != nil {
			return observedMatchFrame{}, err
		}
		if r.Error != "" {
			return observedMatchFrame{}, fmt.Errorf("capture failed: %s", r.Error)
		}
		img, err := readImage(r.Screenshot)
		if err != nil {
			return observedMatchFrame{}, err
		}
		if img.Bounds() != image.Rect(0, 0, 1920, 1080) {
			return observedMatchFrame{}, fmt.Errorf("unvalidated capture geometry")
		}
		at, err := time.Parse(time.RFC3339Nano, r.CapturedAt)
		if err != nil {
			return observedMatchFrame{}, err
		}
		frame := observedMatchFrame{navFrame: navFrame{Reference: paths[0], Captured: at}, Plays: map[string]string{}}
		for _, w := range r.Windows {
			if w.Handle == r.Foreground {
				frame.Width, frame.Height = int(w.Client.Right-w.Client.Left), int(w.Client.Bottom-w.Client.Top)
			}
		}
		frame.Phase, frame.Scores = cfg.model.Recognize(img)
		if frame.Phase == "turn" {
			frame.Hand, err = scanHandFrame(cfg.Hand.scanConfig(false), cfg.Hand.matcher, img, true)
			if err != nil {
				return observedMatchFrame{}, err
			}
		}
		for _, p := range cfg.Plays {
			frame.Plays[p.ID], _ = p.model.Recognize(img)
		}
		return frame, nil
	}
	send := func(ctx context.Context, frame observedMatchFrame, input observedMatchInput, receipt func(InputReceipt) error) error {
		a := input.Action
		a.Reference, a.StopFile = frame.Reference, cfg.StopFile
		a.PhaseModel, a.phaseModel, a.ExpectedPhase = cfg.ModelPath, cfg.model, frame.Phase
		if input.PlayID != "" {
			found := false
			for _, p := range cfg.Plays {
				if p.ID != input.PlayID {
					continue
				}
				found = true
				a.PhaseModel, a.phaseModel, a.ExpectedPhase = p.ModelPath, p.model, frame.Plays[p.ID]
				if input.Name == "select_card" {
					a.extraPhaseModel, a.extraExpectedPhase = cfg.model, "turn"
					cardID := p.CardID
					a.imageGuard = func(img image.Image) error { return guardObservedCard(img, cfg.Hand, input.Slot, cardID) }
				}
			}
			if !found {
				return fmt.Errorf("unknown play profile")
			}
		}
		a.primitiveOnly, a.onReceipt = true, receipt
		a.deadline = frame.Captured.Add(2 * time.Second)
		return ExecuteContext(ctx, out, nativeLogger, a)
	}
	wait := func(ctx context.Context) error {
		timer := time.NewTimer(250 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return nil
		}
	}
	var lastProgress time.Time
	progress := func(e observedMatchEvent) error {
		if e.Status == "observed" || e.Status == "stale_observation_discarded" {
			if time.Since(lastProgress) < 5*time.Second {
				return nil
			}
			lastProgress = time.Now()
		}
		phase, action := "", ""
		if len(e.Frames) > 0 {
			phase = e.Frames[len(e.Frames)-1].Phase
		}
		if e.Input != nil {
			action = e.Input.Name
		}
		_, err := fmt.Fprintf(output, "%s status=%s round=%d plays=%d phase=%s action=%s error=%s\n", e.Time.Format(time.RFC3339Nano), e.Status, e.Round, e.Plays, phase, action, e.Error)
		return err
	}
	return runObservedMatch(ctx, cfg, observedMatchHost{Observe: capture, Send: send, Pause: func() error { return stopped(cfg.StopFile) }, Wait: wait, Progress: progress}, journal)
}
