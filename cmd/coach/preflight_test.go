package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gwentcoachmvp/internal/observe"
	"gwentcoachmvp/internal/uiphase"
)

func writeCoachJSON(t *testing.T, path string, value any) string {
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

func coachPreflightFixture(t *testing.T) (string, observe.NativeSessionConfig) {
	t.Helper()
	root := t.TempDir()
	anchor := uiphase.Anchor{Width: 16, Height: 8, Pixels: make([]float64, 16*8*3), MaxError: .01}
	model := uiphase.PhaseModel{Width: 1920, Height: 1080}
	for _, name := range []string{"home", "training", "mulligan", "turn", "result"} {
		model.Phases = append(model.Phases, uiphase.Phase{Name: name, Anchors: []uiphase.Anchor{anchor, anchor}})
	}
	modelSHA := writeCoachJSON(t, filepath.Join(root, "model.json"), model)
	click := observe.Action{Kind: "click", X: 100, Y: 100}
	loop := observe.NativeLoopConfig{ModelPath: "model.json", ModelSHA256: modelSHA}
	loopSHA := writeCoachJSON(t, filepath.Join(root, "loop.json"), loop)
	cfg := observe.NativeSessionConfig{
		ID: "cli-preflight", StateDir: "nested/state", LoopConfig: "loop.json", LoopConfigSHA256: loopSHA,
		StartMatch: 1, Matches: 1, TransitionTimeoutMS: 1000,
		Bootstrap: []observe.SessionTransition{{From: "home", To: "training", Input: click}},
		Entry:     []observe.SessionTransition{{From: "training", To: "mulligan", Input: click}, {From: "mulligan", To: "turn", Input: click}},
		Exit:      []observe.SessionTransition{{From: "result", To: "training", Input: click}},
	}
	path := filepath.Join(root, "session.json")
	writeCoachJSON(t, path, cfg)
	return path, cfg
}

func runCoach(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	cmd := exec.Command("go", append([]string{"run", "."}, args...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

func TestPreflightSessionCLI(t *testing.T) {
	path, cfg := coachPreflightFixture(t)
	stdout, stderr, err := runCoach(t, "-preflight-session", path)
	if err != nil {
		t.Fatalf("preflight failed: %v\n%s", err, stderr)
	}
	var report observe.SessionPreflightReport
	if err = json.Unmarshal([]byte(stdout), &report); err != nil {
		t.Fatalf("single JSON report: %v\n%s", err, stdout)
	}
	if !report.OK || report.SessionID != cfg.ID || report.LoopConfig != filepath.Join(filepath.Dir(path), "loop.json") || report.ModelPath != filepath.Join(filepath.Dir(path), "model.json") || len(report.PhasesUsed) != 5 || report.TransitionCounts.Entry != 2 {
		t.Fatalf("unexpected report: %+v", report)
	}
	if _, err = os.Stat(filepath.Join(filepath.Dir(path), cfg.StateDir)); !os.IsNotExist(err) {
		t.Fatalf("preflight created state: %v", err)
	}
	if stderr != "" {
		t.Fatalf("unexpected stderr: %s", stderr)
	}
}

func TestPreflightSessionCLIRejectsLegacyAndFlagCombinations(t *testing.T) {
	path, cfg := coachPreflightFixture(t)
	loopPath := filepath.Join(filepath.Dir(path), cfg.LoopConfig)
	legacy := observe.NativeLoopConfig{ModelPath: "model.json", ModelSHA256: strings.Repeat("0", 64), LegacyScenarioStates: true, States: map[string]observe.LoopState{"result": {}}}
	cfg.LoopConfigSHA256 = writeCoachJSON(t, loopPath, legacy)
	writeCoachJSON(t, path, cfg)
	stdout, stderr, err := runCoach(t, "-preflight-session", path)
	if err == nil || !strings.Contains(stderr, "legacy scenario states") {
		t.Fatalf("legacy config accepted: err=%v stdout=%q stderr=%q", err, stdout, stderr)
	}
	var report observe.SessionPreflightReport
	if decodeErr := json.Unmarshal([]byte(stdout), &report); decodeErr != nil || report.OK || !strings.Contains(report.Error, "legacy scenario states") {
		t.Fatalf("failure report: %+v, %v, stdout=%q", report, decodeErr, stdout)
	}

	out := filepath.Join(t.TempDir(), "runs")
	stdout, stderr, err = runCoach(t, "-preflight-session", path, "-out", out)
	if err == nil || stdout != "" || !strings.Contains(stderr, "must be used alone") {
		t.Fatalf("flag combination accepted: err=%v stdout=%q stderr=%q", err, stdout, stderr)
	}
	if _, statErr := os.Stat(out); !os.IsNotExist(statErr) {
		t.Fatalf("guarded mode created output: %v", statErr)
	}
}
