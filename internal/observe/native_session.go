package observe

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
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

// SessionPreflightReport describes the pinned configuration, not live readiness.
type SessionPreflightReport struct {
	OK               bool     `json:"ok"`
	Error            string   `json:"error,omitempty"`
	SessionID        string   `json:"session_id,omitempty"`
	LoopConfig       string   `json:"loop_config"`
	LoopConfigSHA256 string   `json:"loop_config_sha256"`
	ModelPath        string   `json:"model_path"`
	ModelSHA256      string   `json:"model_sha256"`
	PhasesUsed       []string `json:"phases_used"`
	TransitionCounts struct {
		Bootstrap int `json:"bootstrap"`
		Entry     int `json:"entry"`
		Exit      int `json:"exit"`
	} `json:"transition_counts"`
}

type validatedSession struct {
	cfg     NativeSessionConfig
	profile []byte
	loop    NativeLoopConfig
	model   *PhaseModel
	report  SessionPreflightReport
}

// PreflightSessionConfig reads and validates a session and its pinned phase model.
// It creates no files, acquires no interlock, and does not inspect the live game.
func PreflightSessionConfig(path string) (SessionPreflightReport, error) {
	session, err := validateSessionConfig(path)
	if err != nil {
		return SessionPreflightReport{}, err
	}
	return session.report, nil
}

func validateSessionConfig(path string) (*validatedSession, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read session config: %w", err)
	}
	var cfg NativeSessionConfig
	if err = json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("decode session config: %w", err)
	}
	resolve := func(p string) string {
		if filepath.IsAbs(p) {
			return p
		}
		return filepath.Join(filepath.Dir(path), p)
	}
	if !safeSessionID(cfg.ID) || cfg.StateDir == "" || cfg.LoopConfig == "" || cfg.Matches < 1 || cfg.StartMatch < 1 || cfg.TransitionTimeoutMS <= 0 || len(cfg.Entry) == 0 || len(cfg.Exit) == 0 {
		return nil, fmt.Errorf("complete native session configuration required")
	}
	cfg.StateDir = resolve(cfg.StateDir)
	cfg.LoopConfig = resolve(cfg.LoopConfig)
	profile, err := os.ReadFile(cfg.LoopConfig)
	if err != nil {
		return nil, fmt.Errorf("read loop profile: %w", err)
	}
	sum := sha256.Sum256(profile)
	loopSHA := hex.EncodeToString(sum[:])
	if len(cfg.LoopConfigSHA256) != 64 || !strings.EqualFold(cfg.LoopConfigSHA256, loopSHA) {
		return nil, fmt.Errorf("loop profile hash mismatch")
	}
	var loop NativeLoopConfig
	if err = json.Unmarshal(profile, &loop); err != nil {
		return nil, fmt.Errorf("decode loop profile: %w", err)
	}
	if loop.LegacyScenarioStates || len(loop.States) != 0 {
		return nil, fmt.Errorf("training sessions require observed game facts, not legacy scenario states")
	}
	if loop.ResumeJournal || loop.ResumeCard != "" || loop.ResumeOrderCard != "" || loop.ResumeTargetCard != "" {
		return nil, fmt.Errorf("session recovery comes from journal, not loop profile")
	}
	if cfg.Entry[0].From != "training" || cfg.Entry[len(cfg.Entry)-1].To != "turn" || cfg.Exit[0].From != "result" || cfg.Exit[len(cfg.Exit)-1].To != "training" {
		return nil, fmt.Errorf("session requires training-to-turn and result-to-training paths")
	}
	if len(cfg.Bootstrap) > 0 && cfg.Bootstrap[len(cfg.Bootstrap)-1].To != "training" {
		return nil, fmt.Errorf("bootstrap must end at training")
	}
	for _, path := range [][]SessionTransition{cfg.Bootstrap, cfg.Entry, cfg.Exit} {
		for i, t := range path {
			if t.From == "" || t.To == "" || t.From == t.To || (i > 0 && path[i-1].To != t.From) || t.Input.Kind != "click" {
				return nil, fmt.Errorf("invalid calibrated session navigation path")
			}
		}
	}
	if !filepath.IsAbs(loop.ModelPath) {
		loop.ModelPath = filepath.Join(filepath.Dir(cfg.LoopConfig), loop.ModelPath)
	}
	model, err := loadBoundPhases(loop.ModelPath, loop.ModelSHA256)
	if err != nil {
		return nil, fmt.Errorf("load session phase model: %w", err)
	}
	names := map[string]bool{}
	for _, p := range model.Phases {
		names[p.Name] = true
	}
	used := map[string]bool{}
	for _, path := range []struct {
		name  string
		steps []SessionTransition
	}{{"bootstrap", cfg.Bootstrap}, {"entry", cfg.Entry}, {"exit", cfg.Exit}} {
		for i, t := range path.steps {
			if !names[t.From] || !names[t.To] {
				return nil, fmt.Errorf("session %s transition %d: phase %q or %q missing from model", path.name, i+1, t.From, t.To)
			}
			if t.Input.X <= 0 || t.Input.X >= 1920 || t.Input.Y <= 0 || t.Input.Y >= 1080 {
				return nil, fmt.Errorf("session %s transition %d: click coordinates (%d,%d) outside supported bounds (0 < x < 1920, 0 < y < 1080)", path.name, i+1, t.Input.X, t.Input.Y)
			}
			used[t.From], used[t.To] = true, true
		}
	}
	report := SessionPreflightReport{OK: true, SessionID: cfg.ID, LoopConfig: cfg.LoopConfig, LoopConfigSHA256: loopSHA, ModelPath: loop.ModelPath, ModelSHA256: loop.ModelSHA256}
	for name := range used {
		report.PhasesUsed = append(report.PhasesUsed, name)
	}
	sort.Strings(report.PhasesUsed)
	report.TransitionCounts.Bootstrap = len(cfg.Bootstrap)
	report.TransitionCounts.Entry = len(cfg.Entry)
	report.TransitionCounts.Exit = len(cfg.Exit)
	return &validatedSession{cfg: cfg, profile: profile, loop: loop, model: model, report: report}, nil
}

// RunSessionConfig is shared by the Windows CLI and offline host tests. The
// factory is called only after the session, loop pin and phase model are checked.
func RunSessionConfig(ctx context.Context, path string, factory func(NativeSessionConfig) (SessionHost, error)) error {
	return runSessionConfig(ctx, path, func(session *validatedSession) (SessionHost, error) {
		return factory(session.cfg)
	})
}

func runSessionConfig(ctx context.Context, path string, factory func(*validatedSession) (SessionHost, error)) error {
	session, err := validateSessionConfig(path)
	if err != nil {
		return err
	}
	cfg := session.cfg
	host, err := factory(session)
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
