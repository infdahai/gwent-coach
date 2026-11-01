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
	"gwentcoachmvp/internal/cards"
	"gwentcoachmvp/internal/rules"
)

func TestRehearseTurn(t *testing.T) {
	dir := t.TempDir()
	catalogPath, err := filepath.Abs("../../data/cards/14.10.0/en/cards.json")
	if err != nil {
		t.Fatal(err)
	}
	version := cardindex.Version{Data: cardindex.DataVersion, Rules: cardindex.RulesVersion}
	dbPath := filepath.Join(dir, "cards.db")
	if err := cardindex.Import(dbPath, version, []string{catalogPath}); err != nil {
		t.Fatal(err)
	}
	img := image.NewGray(image.Rect(0, 0, 180, 160))
	for y := 0; y < 160; y++ {
		for x := 0; x < 180; x++ {
			img.SetGray(x, y, color.Gray{Y: uint8(x)})
		}
	}
	roi := cardmatch.ROI{X: 0, Y: 0, Width: 180, Height: 160}
	feature, err := cardmatch.Normalize(img, roi)
	if err != nil {
		t.Fatal(err)
	}
	opposite := feature
	for i := range opposite.Gray {
		opposite.Gray[i] = 255 - opposite.Gray[i]
	}
	writeJSON := func(name string, v any) {
		t.Helper()
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), b, 0600); err != nil {
			t.Fatal(err)
		}
	}
	writeJSON("templates.json", cardmatch.File{Version: version, FeatureVersion: cardmatch.FeatureVersion, Templates: []cardmatch.Template{{CardID: 152405, Feature: feature}, {CardID: 113310, Feature: opposite}}})
	for _, name := range []string{"a.png", "b.png"} {
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
	}
	catalog, err := cards.Load(catalogPath)
	if err != nil {
		t.Fatal(err)
	}
	engine, err := rules.New(catalog)
	if err != nil {
		t.Fatal(err)
	}
	state, err := engine.Initial([2][]int{{152405}, {}}, [2][]int{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	view, err := state.View(0)
	if err != nil {
		t.Fatal(err)
	}
	row := image.Pt(40, 40)
	m := turnRehearsal{Database: dbPath, Templates: "templates.json", Catalog: catalogPath, MatchID: "offline", View: view, HandOrder: []string{state.Cards[0].ID}, MaxDistance: 0, MinGap: 1, Rows: [2]*image.Point{&row, &row}}
	for i, name := range []string{"a.png", "b.png"} {
		m.Frames[i].Image = name
		m.Frames[i].Cards = append(m.Frames[i].Cards, struct {
			Instance string
			Art      cardmatch.ROI
			Click    image.Rectangle
		}{state.Cards[0].ID, roi, image.Rect(10, 20, 30, 40)})
	}
	writeJSON("turn.json", m)
	var output bytes.Buffer
	if err := rehearseTurn(filepath.Join(dir, "turn.json"), &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), `"status":"offline_unvalidated_no_input"`) || !strings.Contains(output.String(), `"Card":{"X":20,"Y":30}`) {
		t.Fatal(output.String())
	}
	m.Frames[1].Cards[0].Click = image.Rect(11, 20, 30, 40)
	writeJSON("turn.json", m)
	if err := rehearseTurn(filepath.Join(dir, "turn.json"), &output); err == nil || !strings.Contains(err.Error(), "inconsistent click rectangle") {
		t.Fatalf("unexpected error: %v", err)
	}
}
