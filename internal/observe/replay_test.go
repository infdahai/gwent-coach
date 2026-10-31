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
