package observe

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func TestDesaturatedAnchorIgnoresBrightnessAndRejectsColor(t *testing.T) {
	gray := image.NewRGBA(image.Rect(0, 0, 32, 16))
	colored := image.NewRGBA(gray.Bounds())
	for y := 0; y < 16; y++ {
		for x := 0; x < 32; x++ {
			shade := uint8(80 + x*3)
			gray.Set(x, y, color.RGBA{shade, shade, shade, 255})
			colored.Set(x, y, color.RGBA{200, 60, 30, 255})
		}
	}
	a := Anchor{X: 0, Y: 0, Width: 16, Height: 8, Mode: "desaturated", MaxError: .15, Pixels: make([]float64, 16*8*3)}
	m := PhaseModel{Width: 32, Height: 16, Phases: []Phase{{Name: "selected", Anchors: []Anchor{a, a}}}}
	if err := m.Validate(); err != nil {
		t.Fatal(err)
	}
	if name, _ := m.Recognize(gray); name != "selected" {
		t.Fatalf("gray=%s", name)
	}
	if name, _ := m.Recognize(colored); name != "unknown" {
		t.Fatalf("colored=%s", name)
	}
	if name, _ := m.Recognize(image.NewRGBA(gray.Bounds())); name != "unknown" {
		t.Fatalf("dark=%s", name)
	}
	if got := phaseAnchorDiagnostics(&m, gray)[0].Error; got != 0 {
		t.Fatalf("diagnostic=%v", got)
	}
	a.Mode, a.MaxError = "saturated", .7
	m.Phases[0].Anchors = []Anchor{a, a}
	if err := m.Validate(); err != nil {
		t.Fatal(err)
	}
	if name, _ := m.Recognize(colored); name != "selected" {
		t.Fatalf("colored saturation=%s", name)
	}
	if name, _ := m.Recognize(gray); name != "unknown" {
		t.Fatalf("gray saturation=%s", name)
	}
	if name, _ := m.Recognize(image.NewRGBA(gray.Bounds())); name != "unknown" {
		t.Fatalf("dark saturation=%s", name)
	}
}

func TestReplayPhases(t *testing.T) {
	dir := t.TempDir()
	img := image.NewRGBA(image.Rect(0, 0, 32, 16))
	for y := 0; y < 16; y++ {
		for x := 0; x < 32; x++ {
			img.Set(x, y, color.White)
		}
	}
	anchor := Anchor{X: 0, Y: 0, Width: 16, Height: 8, MaxError: 0.01}
	anchor.Pixels = AnchorPixels(img, anchor, 32, 16)
	model := PhaseModel{Width: 32, Height: 16, Phases: []Phase{{Name: "mulligan", Anchors: []Anchor{anchor, anchor}}}}
	diagnostics := phaseAnchorDiagnostics(&model, img)
	if len(diagnostics) != 2 || diagnostics[0].Error != 0 || diagnostics[0].MaxError != anchor.MaxError {
		t.Fatalf("unexpected anchor diagnostics: %+v", diagnostics)
	}
	black := image.NewRGBA(img.Bounds())
	diagnostics = phaseAnchorDiagnostics(&model, black)
	if diagnostics[0].Error != 1 {
		t.Fatalf("unexpected changed anchor score: %+v", diagnostics[0])
	}
	b, err := json.Marshal(model)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "model.json"), b, 0600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(dir, "frame.png"))
	if err != nil {
		t.Fatal(err)
	}
	if err = png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	manifest := `{"frames":[{"image":"frame.png","expected":"mulligan"},{"image":"frame.png","expected":"unknown"}]}`
	if err = os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(manifest), 0600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err = ReplayPhases(filepath.Join(dir, "model.json"), filepath.Join(dir, "manifest.json"), &output); err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(&output)
	var first, second ReplayResult
	if err = dec.Decode(&first); err != nil {
		t.Fatal(err)
	}
	if err = dec.Decode(&second); err != nil {
		t.Fatal(err)
	}
	if !first.Correct || second.Correct || first.Observed != "mulligan" {
		t.Fatalf("unexpected results: %+v %+v", first, second)
	}
}
