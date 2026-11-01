package main

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gwentcoachmvp/internal/cardindex"
	"gwentcoachmvp/internal/cardmatch"
)

func TestCLI(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "cards.db")
	templatePath := filepath.Join(dir, "templates.json")
	manifestPath := filepath.Join(dir, "samples.json")
	invoke := func(args ...string) (map[string]json.RawMessage, error) {
		t.Helper()
		var output bytes.Buffer
		err := run(append([]string{"-db", dbPath}, args...), &output)
		if err != nil {
			return nil, err
		}
		var result map[string]json.RawMessage
		if err := json.Unmarshal(output.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result, nil
	}
	result, err := invoke("-mode", "import", "-sources", "../../data/cards/14.10.0/en/cards.json,../../data/cards/14.10.0/cn/cards.json")
	if err != nil || string(result["cards"]) != "1381" {
		t.Fatal(result, err)
	}
	result, err = invoke("-mode", "card", "-id", "113301")
	if err != nil || string(result["supported"]) != "true" || string(result["handler"]) != `"native-v1/damage"` {
		t.Fatal(result, err)
	}
	result, err = invoke("-mode", "card", "-id", "112101")
	if err != nil || string(result["supported"]) != "false" || string(result["handler"]) != `""` {
		t.Fatal(result, err)
	}
	result, err = invoke("-mode", "card", "-id", "999")
	if err != nil || string(result["status"]) != `"unknown"` {
		t.Fatal(result, err)
	}
	m := manifest{DataVersion: cardindex.DataVersion, RulesVersion: cardindex.RulesVersion}
	for n, id := range []int{113301, 113310} {
		img := image.NewGray(image.Rect(0, 0, 90, 80))
		for y := 0; y < 80; y++ {
			for x := 0; x < 90; x++ {
				v := uint8(x * 2)
				if n == 1 {
					v = 255 - v
				}
				img.SetGray(x, y, color.Gray{Y: v})
			}
		}
		name := "forward.png"
		if n == 1 {
			name = "reverse.png"
		}
		f, err := os.Create(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if err := png.Encode(f, img); err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
		m.Samples = append(m.Samples, sample{CardID: id, Image: name, ROI: cardmatch.ROI{Width: 90, Height: 80}})
	}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, b, 0600); err != nil {
		t.Fatal(err)
	}
	result, err = invoke("-mode", "templates", "-manifest", manifestPath, "-templates", templatePath)
	if err != nil || string(result["templates"]) != "2" {
		t.Fatal(result, err)
	}
	if _, err := invoke("-mode", "templates", "-manifest", manifestPath, "-templates", templatePath); err == nil {
		t.Fatal("overwrote template index")
	}
	args := []string{"-mode", "image", "-templates", templatePath, "-image", filepath.Join(dir, "forward.png"), "-roi", "0,0,90,80"}
	if _, err := invoke(args...); err == nil {
		t.Fatal("missing thresholds accepted")
	}
	args = append(args, "-max-distance", "0", "-min-gap", "1")
	result, err = invoke(args...)
	if err != nil {
		t.Fatal(err)
	}
	var match cardmatch.Result
	if err := json.Unmarshal(result["result"], &match); err != nil {
		t.Fatal(err)
	}
	if match.Status != "candidate" || match.CardID != 113301 || match.Calibration != "unvalidated" {
		t.Fatal(match)
	}
	result, err = invoke(append(args, "-deck", "113310")...)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(result["result"], &match); err != nil {
		t.Fatal(err)
	}
	if match.Status != "unknown" || match.Reason != "absolute_distance" {
		t.Fatal(match)
	}
	if _, err := invoke(append(args, "-deck", "")...); err == nil {
		t.Fatal("empty deck accepted")
	}
	if _, err := invoke(append(args, "-roi", "0,0,999,999")...); err == nil {
		t.Fatal("invalid ROI accepted")
	}
	// Evaluate known, deliberately wrong, and unknown/occluded annotations.
	m.Samples = append(m.Samples, sample{CardID: 113310, Image: "forward.png", ROI: cardmatch.ROI{Width: 90, Height: 80}}, sample{CardID: 0, Image: "forward.png", ROI: cardmatch.ROI{Width: 90, Height: 80}})
	b, err = json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(manifestPath, b, 0600); err != nil {
		t.Fatal(err)
	}
	result, err = invoke("-mode", "evaluate", "-manifest", manifestPath, "-templates", templatePath, "-max-distance", "0", "-min-gap", "1")
	if err != nil || string(result["correct"]) != "2" || string(result["false_match"]) != "2" {
		t.Fatal(result, err)
	}
	blank, err := os.Create(filepath.Join(dir, "occluded.png"))
	if err != nil {
		t.Fatal(err)
	}
	if err = png.Encode(blank, image.NewGray(image.Rect(0, 0, 90, 80))); err != nil {
		t.Fatal(err)
	}
	if err = blank.Close(); err != nil {
		t.Fatal(err)
	}
	m.Samples = []sample{{CardID: 0, Image: "occluded.png", ROI: cardmatch.ROI{Width: 90, Height: 80}}, {CardID: 113301, Image: "reverse.png", ROI: cardmatch.ROI{Width: 90, Height: 80}}}
	b, err = json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(manifestPath, b, 0600); err != nil {
		t.Fatal(err)
	}
	result, err = invoke("-mode", "evaluate", "-manifest", manifestPath, "-templates", templatePath, "-max-distance", "0", "-min-gap", "1")
	if err != nil || string(result["rejected_unknown"]) != "1" || string(result["false_match"]) != "1" {
		t.Fatal(result, err)
	}
	if _, err = invoke("-mode", "evaluate", "-manifest", manifestPath, "-templates", templatePath); err == nil {
		t.Fatal("accepted missing thresholds")
	}
	if _, err := invoke("-mode", "card", "-data-version", "other"); err == nil || !strings.Contains(err.Error(), "version") {
		t.Fatal(err)
	}
}
