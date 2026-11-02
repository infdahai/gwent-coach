//go:build windows

package observe

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"

	"gwentcoachmvp/internal/uiphase"
)

type windowsSessionHost struct {
	cfg     NativeSessionConfig
	profile []byte
	loop    NativeLoopConfig
	out     string
	logger  *log.Logger
	output  io.Writer
	model   *PhaseModel
	stream  string
	capture func(context.Context) (loopFrame, error)
}

func RunNativeSession(out string, logger *log.Logger, path string, output io.Writer) error {
	return runSessionConfig(context.Background(), path, func(session *validatedSession) (SessionHost, error) {
		cfg, profile, loop, model := session.cfg, session.profile, session.loop, session.model
		if err := runNativeLoopAttempt(context.Background(), out, logger, cfg.LoopConfig, output, nil, &nativeLoopBinding{MatchID: cfg.ID + "/preflight", Preflight: true, Profile: profile}); err != nil {
			return nil, err
		}
		data, err := os.ReadFile(cfg.LoopConfig)
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(data)
		if !strings.EqualFold(hex.EncodeToString(sum[:]), cfg.LoopConfigSHA256) {
			return nil, fmt.Errorf("loop profile changed during preflight")
		}
		if err = acquireInterlock(`D:\gwent-coach-training-local\training-autoplay-interlock`); err != nil {
			return nil, err
		}
		return &windowsSessionHost{cfg: cfg, profile: profile, loop: loop, out: out, logger: logger, output: output, model: model}, nil
	})
}

func (h *windowsSessionHost) ObserveStage(ctx context.Context, id string) (uiphase.Event, error) {
	if h.stream != id {
		cfg := h.loop
		cfg.MatchID = id
		capture, err := newNativeCapture(h.out, h.logger, cfg, h.model)
		if err != nil {
			return uiphase.Event{}, err
		}
		h.capture, h.stream = capture, id
	}
	frame, err := h.capture(ctx)
	if err != nil {
		return uiphase.Event{}, err
	}
	return *frame.Stage, nil
}

func (h *windowsSessionHost) ExecuteInput(ctx context.Context, action Action) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	action.StopFile = h.loop.StopFile
	action.PhaseModel = h.loop.ModelPath
	action.phaseModel = h.model
	action.deadline, _ = ctx.Deadline()
	return Execute(h.out, h.logger, action)
}

func (h *windowsSessionHost) RunMatch(ctx context.Context, id string, journal *ActionJournal) (SemanticObservation, error) {
	data, err := os.ReadFile(h.cfg.LoopConfig)
	if err != nil {
		return SemanticObservation{}, err
	}
	sum := sha256.Sum256(data)
	if !strings.EqualFold(hex.EncodeToString(sum[:]), h.cfg.LoopConfigSHA256) {
		return SemanticObservation{}, fmt.Errorf("loop profile changed during session")
	}
	out := filepath.Join(h.out, "matches", filepath.Base(id))
	if err = os.MkdirAll(out, 0700); err != nil {
		return SemanticObservation{}, err
	}
	var result SemanticObservation
	err = runNativeLoopAttempt(ctx, out, h.logger, h.cfg.LoopConfig, h.output, nil, &nativeLoopBinding{MatchID: id, Journal: journal, Profile: h.profile, OnResult: func(o SemanticObservation) { result = o }, OnRoundStart: func(ctx context.Context, o SemanticObservation) error {
		return configuredSession(h.cfg, h).FinishMulligan(ctx, id, o.View.Round, journal)
	}})
	return result, err
}
