package main

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"gwentcoachmvp/internal/cardindex"
	"gwentcoachmvp/internal/cardmatch"
	"gwentcoachmvp/internal/observe"
)

func TestSavedFrames(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "cards.db")
	v := cardindex.Version{Data: cardindex.DataVersion, Rules: cardindex.RulesVersion}
	source := "../../data/cards/14.10.0/en/cards.json"
	if _, err := os.Stat(source); os.IsNotExist(err) {
		t.Skip("local card snapshot not installed")
	}
	if err := cardindex.Import(db, v, []string{source}); err != nil {
		t.Fatal(err)
	}
	img := image.NewGray(image.Rect(0, 0, 90, 80))
	reverse := image.NewGray(img.Bounds())
	for y := 0; y < 80; y++ {
		for x := 0; x < 90; x++ {
			img.SetGray(x, y, color.Gray{Y: uint8(x)})
			reverse.SetGray(x, y, color.Gray{Y: 255 - uint8(x)})
		}
	}
	roi := cardmatch.ROI{Width: 90, Height: 80}
	f1, _ := cardmatch.Normalize(img, roi)
	f2, _ := cardmatch.Normalize(reverse, roi)
	writeJSON := func(name string, v any) string {
		p := filepath.Join(dir, name)
		b, e := json.Marshal(v)
		if e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(p, b, 0600); e != nil {
			t.Fatal(e)
		}
		return p
	}
	writeJSON("templates.json", cardmatch.File{Version: v, FeatureVersion: cardmatch.FeatureVersion, Templates: []cardmatch.Template{{CardID: 152405, Feature: f1}, {CardID: 113310, Feature: f2}}})
	config := writeJSON("config.json", observe.HandScanConfig{Database: "cards.db", Templates: "templates.json", Width: 90, Height: 80, Slots: []cardmatch.ROI{roi}, MinGap: 1})
	p := filepath.Join(dir, "frame.png")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	if err = png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
	f.Close()
	var out bytes.Buffer
	if err = run([]string{config, p, "r1", p, "r2"}, &out); err != nil {
		t.Fatal(err)
	}
	var result struct {
		Cards []observe.HandCandidate `json:"cards"`
	}
	if err = json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Cards) != 1 || result.Cards[0].CardID != 152405 || result.Cards[0].Status != "candidate" {
		t.Fatalf("%+v", result)
	}
}
