package observe

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gwentcoachmvp/internal/cardindex"
	"gwentcoachmvp/internal/cardmatch"
)

func observedConfigFixture(t *testing.T) (string, ObservedMatchConfig, func(string, any) string) {
	t.Helper()
	dir := t.TempDir()
	write := func(name string, v any) string {
		t.Helper()
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(dir, name), b, 0600); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(b)
		return hex.EncodeToString(sum[:])
	}
	hand, board := image.Rect(450, 900, 550, 1000), image.Rect(850, 650, 950, 750)
	model := func(names ...string) PhaseModel {
		m := PhaseModel{Width: 1920, Height: 1080}
		for i, name := range names {
			var anchors []Anchor
			for _, roi := range []image.Rectangle{hand, board} {
				pixels := make([]float64, 16*8*3)
				for j := range pixels {
					pixels[j] = float64(i+1) / 10
				}
				anchors = append(anchors, Anchor{X: roi.Min.X, Y: roi.Min.Y, Width: roi.Dx(), Height: roi.Dy(), MaxError: .01, Pixels: pixels})
			}
			m.Phases = append(m.Phases, Phase{Name: name, Anchors: anchors})
		}
		return m
	}
	cfg := matchTestConfig()
	cfg.Kind, cfg.ID, cfg.DeckID = ObservedMatchKind, "test-match", "audited-test-deck"
	cfg.StopFile, cfg.StateDir = filepath.Join(dir, "STOP"), "attempt"
	cfg.ModelPath = "lifecycle.json"
	cfg.ModelSHA256 = write(cfg.ModelPath, model("training", "mulligan", "mulligan-confirm", "turn", "passed", "opponent", "defeat"))
	cfg.Plays[0].ModelPath = "play.json"
	cfg.Plays[0].ModelSHA256 = write("play.json", model("ready", "selected", "ended"))
	cfg.Plays[0].BoardROI = board
	version := cardindex.Version{Data: cardindex.DataVersion, Rules: cardindex.RulesVersion}
	if err := cardindex.Import(filepath.Join(dir, "cards.db"), version, []string{"../../data/cards/14.10.0/en/cards.json"}); err != nil {
		t.Fatal(err)
	}
	var forward, reverse cardmatch.Feature
	for i := range forward.Gray {
		forward.Gray[i] = uint8((i % 9) * 25)
		reverse.Gray[i] = 255 - forward.Gray[i]
	}
	cfg.Hand.Database, cfg.Hand.Templates = "cards.db", "templates.json"
	cfg.Hand.TemplatesSHA256 = write("templates.json", cardmatch.File{Version: version, FeatureVersion: cardmatch.FeatureVersion, Templates: []cardmatch.Template{{CardID: 142306, Feature: forward}, {CardID: 142315, Feature: reverse}}})
	cfg.Hand.Slots = []cardmatch.ROI{{X: 450, Y: 900, Width: 100, Height: 100}, {X: 650, Y: 900, Width: 100, Height: 100}}
	cfg.Hand.GuardSlots = cfg.Hand.Slots
	cfg.Hand.MaxDistance, cfg.Hand.MinGap = 0, 1
	write("match.json", cfg)
	return filepath.Join(dir, "match.json"), cfg, write
}

func TestObservedMatchHandScanAndHoverGuard(t *testing.T) {
	path, _, _ := observedConfigFixture(t)
	cfg, err := LoadObservedMatchConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	for slot := range cfg.Hand.Slots {
		img := image.NewGray(image.Rect(0, 0, 1920, 1080))
		roi := cfg.Hand.Slots[slot]
		for y := roi.Y; y < roi.Y+roi.Height; y++ {
			for x := roi.X; x < roi.X+roi.Width; x++ {
				img.SetGray(x, y, color.Gray{Y: uint8((x - roi.X) * 2)})
			}
		}
		matches, err := scanHandFrame(cfg.Hand.scanConfig(false), cfg.Hand.matcher, img, true)
		if err != nil || matches[slot].CardID != 142306 || matches[1-slot].Status != "unknown" {
			t.Fatalf("matches=%v err=%v", matches, err)
		}
		if err := guardObservedCard(img, cfg.Hand, slot, 142306); err != nil {
			t.Fatal(err)
		}
		if err := guardObservedCard(img, cfg.Hand, slot, 142315); err == nil {
			t.Fatal("guard accepted changed identity")
		}
		if err := guardObservedCard(img, cfg.Hand, 1-slot, 142306); err == nil {
			t.Fatal("guard accepted empty slot")
		}
	}
}

func TestObservedMatchConfigPinsModelsAndRegions(t *testing.T) {
	path, _, write := observedConfigFixture(t)
	loaded, err := LoadObservedMatchConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.model == nil || loaded.Plays[0].model == nil || loaded.configSHA256 == "" || !filepath.IsAbs(loaded.StateDir) {
		t.Fatal("configuration not bound")
	}
	// The host retains parsed model bytes; changing the file invalidates the
	// next run without changing the current attempt's model.
	original := loaded.Plays[0].model.Phases[0].Anchors[0].Pixels[0]
	write("play.json", map[string]string{"changed": "model"})
	if _, err = LoadObservedMatchConfig(path); err == nil || !strings.Contains(err.Error(), "digest mismatch") {
		t.Fatal(err)
	}
	if loaded.Plays[0].model.Phases[0].Anchors[0].Pixels[0] != original {
		t.Fatal("in-memory model changed")
	}
}

func TestObservedMatchConfigRejectsUnsupportedOrUnboundPlay(t *testing.T) {
	for _, mode := range []string{"targeted-card", "relative-stop", "missing-board-anchor", "missing-ended", "wrong-model-hash", "duplicate-profile", "out-of-bounds"} {
		t.Run(mode, func(t *testing.T) {
			path, cfg, write := observedConfigFixture(t)
			switch mode {
			case "targeted-card":
				cfg.Plays[0].CardID = 142205
			case "relative-stop":
				cfg.StopFile = "STOP"
			case "wrong-model-hash":
				cfg.ModelSHA256 = strings.Repeat("0", 64)
			case "duplicate-profile":
				cfg.Plays = append(cfg.Plays, cfg.Plays[0])
			case "out-of-bounds":
				cfg.Plays[0].Row = image.Pt(2000, 700)
			case "missing-board-anchor", "missing-ended":
				m, err := LoadPhases(filepath.Join(filepath.Dir(path), cfg.Plays[0].ModelPath))
				if err != nil {
					t.Fatal(err)
				}
				if mode == "missing-ended" {
					m.Phases = m.Phases[:2]
				} else {
					m.Phases[0].Anchors[1].X++
				}
				cfg.Plays[0].ModelSHA256 = write("play.json", m)
			}
			write("match.json", cfg)
			if _, err := LoadObservedMatchConfig(path); err == nil {
				t.Fatal("accepted invalid configuration")
			}
		})
	}
}
