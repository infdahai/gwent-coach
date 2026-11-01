package observe

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gwentcoachmvp/internal/uiphase"
)

// NativeSessionConfig pins one reusable native-loop profile. Its MatchID and
// attempt directory are supplied by the session for each match.
type NativeSessionConfig struct {
	ID, StateDir, LoopConfig, LoopConfigSHA256 string
	StartMatch, Matches, TransitionTimeoutMS   int
	ResumeFirst                                bool
	Bootstrap, Entry, Exit                     []SessionTransition
}

type SessionHost interface {
	ObserveStage(context.Context, string) (uiphase.Event, error)
	ExecuteInput(context.Context, Action) error
	RunMatch(context.Context, string, *ActionJournal) (SemanticObservation, error)
}

// RunSessionConfig is shared by the Windows CLI and offline host tests. The
// factory is called only after the session config and loop profile are checked.
func RunSessionConfig(ctx context.Context, path string, factory func(NativeSessionConfig) (SessionHost, error)) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var cfg NativeSessionConfig
	if err = json.Unmarshal(data, &cfg); err != nil {
		return err
	}
	resolve := func(p string) string {
		if filepath.IsAbs(p) {
			return p
		}
		return filepath.Join(filepath.Dir(path), p)
	}
	if !safeSessionID(cfg.ID) || cfg.StateDir == "" || cfg.LoopConfig == "" || cfg.Matches < 1 || cfg.StartMatch < 1 || cfg.TransitionTimeoutMS <= 0 || len(cfg.Entry) == 0 || len(cfg.Exit) == 0 {
		return fmt.Errorf("complete native session configuration required")
	}
	cfg.StateDir = resolve(cfg.StateDir)
	cfg.LoopConfig = resolve(cfg.LoopConfig)
	profile, err := os.ReadFile(cfg.LoopConfig)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(profile)
	if len(cfg.LoopConfigSHA256) != 64 || !strings.EqualFold(cfg.LoopConfigSHA256, hex.EncodeToString(sum[:])) {
		return fmt.Errorf("loop profile hash mismatch")
	}
	var loop NativeLoopConfig
	if err = json.Unmarshal(profile, &loop); err != nil {
		return err
	}
	if loop.LegacyScenarioStates || len(loop.States) != 0 {
		return fmt.Errorf("training sessions require observed game facts, not legacy scenario states")
	}
	if loop.ResumeJournal || loop.ResumeCard != "" || loop.ResumeOrderCard != "" || loop.ResumeTargetCard != "" {
		return fmt.Errorf("session recovery comes from journal, not loop profile")
	}
	if cfg.Entry[0].From != "training" || cfg.Entry[len(cfg.Entry)-1].To != "turn" || cfg.Exit[0].From != "result" || cfg.Exit[len(cfg.Exit)-1].To != "training" {
		return fmt.Errorf("session requires training-to-turn and result-to-training paths")
	}
	if len(cfg.Bootstrap) > 0 && cfg.Bootstrap[len(cfg.Bootstrap)-1].To != "training" {
		return fmt.Errorf("bootstrap must end at training")
	}
	for _, path := range [][]SessionTransition{cfg.Bootstrap, cfg.Entry, cfg.Exit} {
		for i, t := range path {
			if t.From == "" || t.To == "" || t.From == t.To || (i > 0 && path[i-1].To != t.From) || t.Input.Kind != "click" {
				return fmt.Errorf("invalid calibrated session navigation path")
			}
		}
	}
	host, err := factory(cfg)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(cfg.StateDir, 0700); err != nil {
		return err
	}
	return configuredSession(cfg, host).Run(ctx)
}

func configuredSession(cfg NativeSessionConfig, host SessionHost) TrainingSession {
	return TrainingSession{ID: cfg.ID, LogRoot: cfg.StateDir, StartMatch: cfg.StartMatch, Matches: cfg.Matches, ResumeFirst: cfg.ResumeFirst, Bootstrap: cfg.Bootstrap, Entry: cfg.Entry, Exit: cfg.Exit, TransitionBudget: time.Duration(cfg.TransitionTimeoutMS) * time.Millisecond, ObserveStage: host.ObserveStage, ExecuteInput: host.ExecuteInput, RunMatch: host.RunMatch}
}
