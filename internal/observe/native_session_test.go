package observe

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gwentcoachmvp/internal/uiphase"
)

func writeSessionJSON(t *testing.T, path string, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func sessionFixture(t *testing.T) (string, NativeSessionConfig, NativeLoopConfig) {
	t.Helper()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "profiles"), 0700); err != nil {
		t.Fatal(err)
	}
	anchor := uiphase.Anchor{Width: 16, Height: 8, Pixels: make([]float64, 16*8*3), MaxError: .01}
	model := PhaseModel{Width: 1920, Height: 1080}
	for _, name := range []string{"home", "training", "mulligan", "turn", "result", "unused"} {
		model.Phases = append(model.Phases, uiphase.Phase{Name: name, Anchors: []uiphase.Anchor{anchor, anchor}})
	}
	modelSHA := writeSessionJSON(t, filepath.Join(root, "model.json"), model)
	loop := NativeLoopConfig{ModelPath: "../model.json", ModelSHA256: modelSHA}
	click := Action{Kind: "click", X: 100, Y: 100}
	cfg := NativeSessionConfig{
		ID: "preflight", StateDir: "state", LoopConfig: "profiles/loop.json",
		StartMatch: 1, Matches: 2, TransitionTimeoutMS: 1000,
		Bootstrap: []SessionTransition{{From: "home", To: "training", Input: click}},
		Entry:     []SessionTransition{{From: "training", To: "mulligan", Input: click}, {From: "mulligan", To: "turn", Input: click}},
		Exit:      []SessionTransition{{From: "result", To: "training", Input: click}},
	}
	return filepath.Join(root, "session.json"), cfg, loop
}

func saveSessionFixture(t *testing.T, path string, cfg NativeSessionConfig, loop NativeLoopConfig) NativeSessionConfig {
	t.Helper()
	cfg.LoopConfigSHA256 = writeSessionJSON(t, filepath.Join(filepath.Dir(path), cfg.LoopConfig), loop)
	writeSessionJSON(t, path, cfg)
	return cfg
}

func assertNoSessionState(t *testing.T, path string, cfg NativeSessionConfig) {
	t.Helper()
	state := cfg.StateDir
	if !filepath.IsAbs(state) {
		state = filepath.Join(filepath.Dir(path), state)
	}
	if _, err := os.Stat(state); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("preflight created state directory or stat failed: %v", err)
	}
}

func TestPreflightSessionConfig(t *testing.T) {
	path, cfg, loop := sessionFixture(t)
	cfg.ResumeFirst = true // Session journal recovery remains supported.
	cfg.Entry[0].Input.X, cfg.Entry[0].Input.Y = 1, 1
	cfg.Exit[0].Input.X, cfg.Exit[0].Input.Y = 1919, 1079
	cfg = saveSessionFixture(t, path, cfg, loop)
	cfg.LoopConfigSHA256 = strings.ToUpper(cfg.LoopConfigSHA256)
	writeSessionJSON(t, path, cfg)
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	relative, err := filepath.Rel(cwd, path)
	if err != nil {
		t.Fatal(err)
	}
	report, err := PreflightSessionConfig(relative)
	if err != nil {
		t.Fatal(err)
	}
	if !report.OK || report.SessionID != cfg.ID || report.LoopConfig != filepath.Join(filepath.Dir(path), cfg.LoopConfig) || report.ModelPath != filepath.Join(filepath.Dir(path), "model.json") || report.LoopConfigSHA256 != strings.ToLower(cfg.LoopConfigSHA256) || report.ModelSHA256 != loop.ModelSHA256 {
		t.Fatalf("unexpected report: %+v", report)
	}
	if !reflect.DeepEqual(report.PhasesUsed, []string{"home", "mulligan", "result", "training", "turn"}) || report.TransitionCounts.Bootstrap != 1 || report.TransitionCounts.Entry != 2 || report.TransitionCounts.Exit != 1 {
		t.Fatalf("unexpected transitions: %+v", report)
	}
	assertNoSessionState(t, path, cfg)

	// The runner receives the same resolved config before creating any state.
	factoryErr := errors.New("stop before running host")
	err = RunSessionConfig(context.Background(), relative, func(resolved NativeSessionConfig) (SessionHost, error) {
		if resolved.StateDir != filepath.Join(filepath.Dir(path), "state") || resolved.LoopConfig != report.LoopConfig {
			t.Errorf("paths not resolved: %+v", resolved)
		}
		return nil, factoryErr
	})
	if !errors.Is(err, factoryErr) {
		t.Fatalf("factory error lost: %v", err)
	}
	assertNoSessionState(t, path, cfg)
}

func TestPreflightSessionRejectsInvalidConfigBeforeHost(t *testing.T) {
	cases := []struct {
		name   string
		change func(*NativeSessionConfig, *NativeLoopConfig)
		want   string
	}{
		{"missing ID", func(c *NativeSessionConfig, _ *NativeLoopConfig) { c.ID = "" }, "complete native session"},
		{"unsafe ID", func(c *NativeSessionConfig, _ *NativeLoopConfig) { c.ID = "../escape" }, "complete native session"},
		{"missing state", func(c *NativeSessionConfig, _ *NativeLoopConfig) { c.StateDir = "" }, "complete native session"},
		{"missing entry", func(c *NativeSessionConfig, _ *NativeLoopConfig) { c.Entry = nil }, "complete native session"},
		{"missing exit", func(c *NativeSessionConfig, _ *NativeLoopConfig) { c.Exit = nil }, "complete native session"},
		{"invalid count", func(c *NativeSessionConfig, _ *NativeLoopConfig) { c.Matches = 0 }, "complete native session"},
		{"invalid start", func(c *NativeSessionConfig, _ *NativeLoopConfig) { c.StartMatch = 0 }, "complete native session"},
		{"invalid timeout", func(c *NativeSessionConfig, _ *NativeLoopConfig) { c.TransitionTimeoutMS = 0 }, "complete native session"},
		{"wrong entry boundary", func(c *NativeSessionConfig, _ *NativeLoopConfig) { c.Entry[0].From = "home" }, "training-to-turn"},
		{"wrong exit boundary", func(c *NativeSessionConfig, _ *NativeLoopConfig) { c.Exit[0].From = "turn" }, "result-to-training"},
		{"wrong bootstrap boundary", func(c *NativeSessionConfig, _ *NativeLoopConfig) { c.Bootstrap[0].To = "turn" }, "bootstrap must end"},
		{"disconnected path", func(c *NativeSessionConfig, _ *NativeLoopConfig) { c.Entry[1].From = "home" }, "invalid calibrated"},
		{"self transition", func(c *NativeSessionConfig, _ *NativeLoopConfig) { c.Bootstrap[0].From = "training" }, "invalid calibrated"},
		{"empty phase", func(c *NativeSessionConfig, _ *NativeLoopConfig) { c.Bootstrap[0].From = "" }, "invalid calibrated"},
		{"non click", func(c *NativeSessionConfig, _ *NativeLoopConfig) { c.Entry[0].Input.Kind = "hover" }, "invalid calibrated"},
		{"missing model", func(_ *NativeSessionConfig, l *NativeLoopConfig) { l.ModelPath = "missing.json" }, "load session phase model"},
		{"missing model SHA", func(_ *NativeSessionConfig, l *NativeLoopConfig) { l.ModelSHA256 = "" }, "expected phase model SHA-256"},
		{"wrong model SHA", func(_ *NativeSessionConfig, l *NativeLoopConfig) { l.ModelSHA256 = strings.Repeat("0", 64) }, "phase model digest mismatch"},
		{"unknown bootstrap phase", func(c *NativeSessionConfig, _ *NativeLoopConfig) { c.Bootstrap[0].From = "absent" }, "phase \"absent\""},
		{"unknown entry phase", func(c *NativeSessionConfig, _ *NativeLoopConfig) {
			c.Entry[0].To = "absent"
			c.Entry[1].From = "absent"
		}, "missing from model"},
		{"unknown exit phase", func(c *NativeSessionConfig, _ *NativeLoopConfig) {
			c.Exit = append([]SessionTransition{{From: "result", To: "absent", Input: c.Exit[0].Input}}, c.Exit...)
			c.Exit[1].From = "absent"
		}, "missing from model"},
		{"zero x", func(c *NativeSessionConfig, _ *NativeLoopConfig) { c.Bootstrap[0].Input.X = 0 }, "outside supported bounds"},
		{"negative x", func(c *NativeSessionConfig, _ *NativeLoopConfig) { c.Entry[0].Input.X = -1 }, "outside supported bounds"},
		{"right edge", func(c *NativeSessionConfig, _ *NativeLoopConfig) { c.Entry[0].Input.X = 1920 }, "outside supported bounds"},
		{"zero y", func(c *NativeSessionConfig, _ *NativeLoopConfig) { c.Exit[0].Input.Y = 0 }, "outside supported bounds"},
		{"negative y", func(c *NativeSessionConfig, _ *NativeLoopConfig) { c.Entry[0].Input.Y = -1 }, "outside supported bounds"},
		{"bottom edge", func(c *NativeSessionConfig, _ *NativeLoopConfig) { c.Entry[0].Input.Y = 1080 }, "outside supported bounds"},
		{"legacy flag", func(_ *NativeSessionConfig, l *NativeLoopConfig) { l.LegacyScenarioStates = true }, "legacy scenario states"},
		{"static states", func(_ *NativeSessionConfig, l *NativeLoopConfig) { l.States = map[string]LoopState{"result": {}} }, "legacy scenario states"},
		{"resume journal", func(_ *NativeSessionConfig, l *NativeLoopConfig) { l.ResumeJournal = true }, "session recovery comes from journal"},
		{"resume card", func(_ *NativeSessionConfig, l *NativeLoopConfig) { l.ResumeCard = "card" }, "session recovery comes from journal"},
		{"resume order", func(_ *NativeSessionConfig, l *NativeLoopConfig) { l.ResumeOrderCard = "card" }, "session recovery comes from journal"},
		{"resume target", func(_ *NativeSessionConfig, l *NativeLoopConfig) { l.ResumeTargetCard = "card" }, "session recovery comes from journal"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path, cfg, loop := sessionFixture(t)
			tc.change(&cfg, &loop)
			saveSessionFixture(t, path, cfg, loop)
			_, err := PreflightSessionConfig(path)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %q, got %v", tc.want, err)
			}
			runErr := RunSessionConfig(context.Background(), path, func(NativeSessionConfig) (SessionHost, error) {
				t.Fatal("invalid configuration reached host factory")
				return nil, nil
			})
			if runErr == nil || runErr.Error() != err.Error() {
				t.Fatalf("runner/preflight mismatch: %v / %v", runErr, err)
			}
			if cfg.StateDir != "" {
				assertNoSessionState(t, path, cfg)
			}
		})
	}
}

func TestPreflightSessionFileFailures(t *testing.T) {
	for _, name := range []string{"missing session", "malformed session", "missing loop", "malformed loop", "wrong loop SHA", "invalid model", "malformed model"} {
		t.Run(name, func(t *testing.T) {
			path, cfg, loop := sessionFixture(t)
			cfg = saveSessionFixture(t, path, cfg, loop)
			loopPath := filepath.Join(filepath.Dir(path), cfg.LoopConfig)
			writeRaw := func(path string) {
				t.Helper()
				if err := os.WriteFile(path, []byte("{"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			remove := func(path string) {
				t.Helper()
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			}
			want := ""
			switch name {
			case "missing session":
				remove(path)
				want = "read session config"
			case "malformed session":
				writeRaw(path)
				want = "decode session config"
			case "missing loop":
				remove(loopPath)
				want = "read loop profile"
			case "malformed loop":
				writeRaw(loopPath)
				sum := sha256.Sum256([]byte("{"))
				cfg.LoopConfigSHA256 = hex.EncodeToString(sum[:])
				writeSessionJSON(t, path, cfg)
				want = "decode loop profile"
			case "wrong loop SHA":
				cfg.LoopConfigSHA256 = strings.Repeat("0", 64)
				writeSessionJSON(t, path, cfg)
				want = "loop profile hash mismatch"
			case "invalid model":
				loop.ModelSHA256 = writeSessionJSON(t, filepath.Join(filepath.Dir(path), "model.json"), PhaseModel{})
				saveSessionFixture(t, path, cfg, loop)
				want = "empty phase model"
			case "malformed model":
				writeRaw(filepath.Join(filepath.Dir(path), "model.json"))
				sum := sha256.Sum256([]byte("{"))
				loop.ModelSHA256 = hex.EncodeToString(sum[:])
				saveSessionFixture(t, path, cfg, loop)
				want = "load session phase model"
			}
			report, err := PreflightSessionConfig(path)
			if err == nil || !strings.Contains(err.Error(), want) || report.OK {
				t.Fatalf("want %q, got report %+v, error %v", want, report, err)
			}
			assertNoSessionState(t, path, cfg)
		})
	}
}
