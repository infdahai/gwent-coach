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

	"gwentcoachmvp/internal/cardindex"
	"gwentcoachmvp/internal/cardmatch"
)

type cliPoint struct{ X, Y int }
type cliRect struct{ Min, Max cliPoint }
type cliROI struct{ X, Y, Width, Height int }
type cliName struct {
	Name   string
	CardID int
}
type cliHand struct {
	Database, Templates, TemplatesSHA256 string
	Slots, GuardSlots                    []cliROI
	Clicks                               []cliPoint
	MaxDistance, MinGap                  int
	Names                                []cliName `json:",omitempty"`
}
type cliPlay struct {
	ID, ModelPath, ModelSHA256 string
	CardID                     int
	Row                        cliPoint
	BoardROI                   cliRect
}
type cliConfig struct {
	Kind, ID, DeckID, ModelPath, ModelSHA256, StopFile, StateDir string
	TimeoutMS, TransitionTimeoutMS, UnknownTimeoutMS             int
	Start, FinishMulligan, ConfirmMulligan, Pass, EndTurn        cliPoint
	Hand                                                         cliHand
	Plays                                                        []cliPlay
}
type cliAnchor struct {
	X, Y, Width, Height int
	Pixels              []float64
	MaxError            float64
}
type cliPhase struct {
	Name    string
	Anchors []cliAnchor
}
type cliPhaseModel struct {
	Width, Height int
	Phases        []cliPhase
}

func preflightCLIFixture(t *testing.T) (string, *cliConfig, func(string, any) string) {
	t.Helper()
	dir := t.TempDir()
	write := func(name string, v any) string {
		t.Helper()
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), b, 0600); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(b)
		return hex.EncodeToString(sum[:])
	}
	hand := cliRect{Min: cliPoint{450, 900}, Max: cliPoint{550, 1000}}
	board := cliRect{Min: cliPoint{850, 650}, Max: cliPoint{950, 750}}
	model := func(names ...string) cliPhaseModel {
		m := cliPhaseModel{Width: 1920, Height: 1080}
		for i, name := range names {
			var anchors []cliAnchor
			for _, r := range []cliRect{hand, board} {
				pixels := make([]float64, 16*8*3)
				for j := range pixels {
					pixels[j] = float64(i+1) / 10
				}
				anchors = append(anchors, cliAnchor{
					X: r.Min.X, Y: r.Min.Y, Width: r.Max.X - r.Min.X, Height: r.Max.Y - r.Min.Y,
					MaxError: .01, Pixels: pixels,
				})
			}
			m.Phases = append(m.Phases, cliPhase{Name: name, Anchors: anchors})
		}
		return m
	}

	cfg := &cliConfig{
		Kind: "observed-match-v1", ID: "cli-preflight", DeckID: "audited-test-deck",
		StopFile: filepath.Join(dir, "STOP"), StateDir: "attempt",
		TimeoutMS: 2000, TransitionTimeoutMS: 500, UnknownTimeoutMS: 20,
		Start: cliPoint{790, 555}, FinishMulligan: cliPoint{870, 1020},
		ConfirmMulligan: cliPoint{892, 635}, Pass: cliPoint{1845, 540}, EndTurn: cliPoint{1845, 540},
		Hand: cliHand{
			Database: "cards.db", Templates: "templates.json",
			Slots:       []cliROI{{450, 900, 100, 100}, {650, 900, 100, 100}},
			GuardSlots:  []cliROI{{450, 900, 100, 100}, {650, 900, 100, 100}},
			Clicks:      []cliPoint{{500, 980}, {700, 980}},
			MaxDistance: 0, MinGap: 1,
		},
		Plays: []cliPlay{{
			ID: "defender", CardID: 142306, Row: cliPoint{900, 700}, BoardROI: board,
		}},
	}
	cfg.ModelPath = "lifecycle.json"
	cfg.ModelSHA256 = write(cfg.ModelPath, model("training", "mulligan", "mulligan-confirm", "turn", "passed", "victory", "defeat", "draw"))
	cfg.Plays[0].ModelPath = "play.json"
	cfg.Plays[0].ModelSHA256 = write("play.json", model("ready", "selected", "ended"))

	version := cardindex.Version{Data: cardindex.DataVersion, Rules: cardindex.RulesVersion}
	cards, err := filepath.Abs(filepath.Join("..", "..", "data", "cards", "14.10.0", "en", "cards.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := cardindex.Import(filepath.Join(dir, "cards.db"), version, []string{cards}); err != nil {
		t.Fatal(err)
	}
	var forward, reverse cardmatch.Feature
	for i := range forward.Gray {
		forward.Gray[i] = uint8((i % 9) * 25)
		reverse.Gray[i] = 255 - forward.Gray[i]
	}
	cfg.Hand.TemplatesSHA256 = write("templates.json", cardmatch.File{
		Version: version, FeatureVersion: cardmatch.FeatureVersion,
		Templates: []cardmatch.Template{{CardID: 142306, Feature: forward}, {CardID: 142315, Feature: reverse}},
	})

	write("match.json", cfg)
	return filepath.Join(dir, "match.json"), cfg, write
}

func buildCoachBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "coach-preflight")
	out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput()
	if err != nil {
		t.Fatalf("build coach: %v\n%s", err, out)
	}
	return bin
}

func runPreflightCLI(t *testing.T, bin, config, workDir string) ([]byte, int) {
	t.Helper()
	cmd := exec.Command(bin, "preflight-observed", config)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	cmd.Dir = workDir
	err := cmd.Run()
	code := 0
	if err != nil {
		exitErr, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("run preflight: %v (%s)", err, stderr.String())
		}
		code = exitErr.ExitCode()
	}
	return stdout.Bytes(), code
}

func decodePreflight(t *testing.T, out []byte) map[string]any {
	t.Helper()
	var report map[string]any
	if err := json.Unmarshal(out, &report); err != nil {
		t.Fatalf("preflight output is not complete JSON: %v\n%s", err, out)
	}
	return report
}

func TestPreflightCLIValidConfig(t *testing.T) {
	bin := buildCoachBinary(t)
	path, _, _ := preflightCLIFixture(t)
	work := t.TempDir()

	out, code := runPreflightCLI(t, bin, path, work)
	report := decodePreflight(t, out)
	if code != 0 {
		t.Fatalf("exit=%d report=%v", code, report)
	}
	if report["offline_only"] != true || report["live_qualified"] != false {
		t.Fatalf("offline/live flags: %v", report)
	}
	if report["load_result"] != "ok" || report["resources_ready"] != true {
		t.Fatalf("unexpected report: %v", report)
	}
	// Preflight must not create the default observation directory.
	if _, err := os.Stat(filepath.Join(work, "runs")); err == nil {
		t.Fatal("preflight created runs directory")
	}
}

func TestPreflightCLIBlocking(t *testing.T) {
	bin := buildCoachBinary(t)
	cases := []struct {
		name   string
		mutate func(dir string, cfg *cliConfig, write func(string, any) string)
	}{
		{"missing victory", func(dir string, cfg *cliConfig, write func(string, any) string) {
			dropPhase(t, dir, cfg, write, "victory")
		}},
		{"missing draw", func(dir string, cfg *cliConfig, write func(string, any) string) {
			dropPhase(t, dir, cfg, write, "draw")
		}},
		{"bad database", func(dir string, cfg *cliConfig, write func(string, any) string) {
			cfg.Hand.Database = "missing.db"
		}},
		{"bad template hash", func(dir string, cfg *cliConfig, write func(string, any) string) {
			cfg.Hand.TemplatesSHA256 = strings.Repeat("0", 64)
		}},
		{"unknown schema", func(dir string, cfg *cliConfig, write func(string, any) string) {
			cfg.Kind = "unknown-v1"
		}},
		{"bad board roi", func(dir string, cfg *cliConfig, write func(string, any) string) {
			cfg.Plays[0].BoardROI = cliRect{Min: cliPoint{0, 0}, Max: cliPoint{10, 10}}
		}},
		{"invalid names", func(dir string, cfg *cliConfig, write func(string, any) string) {
			cfg.Hand.Names = []cliName{{Name: "Wrong Name", CardID: 142306}, {Name: "Also Wrong", CardID: 142315}}
			cfg.Hand.Templates, cfg.Hand.TemplatesSHA256 = "", ""
			cfg.Hand.GuardSlots = nil
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path, cfg, write := preflightCLIFixture(t)
			tc.mutate(filepath.Dir(path), cfg, write)
			write("match.json", cfg)

			out, code := runPreflightCLI(t, bin, path, t.TempDir())
			if code == 0 {
				t.Fatalf("expected non-zero exit, got 0: %s", out)
			}
			report := decodePreflight(t, out)
			if report["load_result"] == "ok" && tc.name != "missing victory" && tc.name != "missing draw" {
				t.Fatalf("expected blocking load_result: %v", report)
			}
			if report["resources_ready"] == true {
				t.Fatalf("resources_ready must be false: %v", report)
			}
		})
	}
}

func TestPreflightCLIDoesNotCreateFiles(t *testing.T) {
	bin := buildCoachBinary(t)
	path, _, _ := preflightCLIFixture(t)
	dir := filepath.Dir(path)
	before, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, e := range before {
		seen[e.Name()] = true
	}

	runPreflightCLI(t, bin, path, t.TempDir())

	after, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("preflight changed config directory: %d -> %d entries", len(before), len(after))
	}
	for _, e := range after {
		if !seen[e.Name()] {
			t.Fatalf("preflight created %s", e.Name())
		}
	}
}

func TestPreflightCLIUsage(t *testing.T) {
	bin := buildCoachBinary(t)
	cmd := exec.Command(bin, "preflight-observed")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	cmd.Dir = t.TempDir()
	err := cmd.Run()
	if err == nil {
		t.Fatal("expected non-zero exit for missing argument")
	}
	if !strings.Contains(stderr.String(), "usage") {
		t.Fatalf("expected usage on stderr, got %q", stderr.String())
	}
}

func dropPhase(t *testing.T, dir string, cfg *cliConfig, write func(string, any) string, name string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, cfg.ModelPath))
	if err != nil {
		t.Fatal(err)
	}
	var model cliPhaseModel
	if err := json.Unmarshal(data, &model); err != nil {
		t.Fatal(err)
	}
	var kept []cliPhase
	for _, p := range model.Phases {
		if p.Name != name {
			kept = append(kept, p)
		}
	}
	model.Phases = kept
	cfg.ModelSHA256 = write(cfg.ModelPath, model)
}
