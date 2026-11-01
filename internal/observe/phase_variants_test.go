package observe

import (
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

type genericImage struct{ image.Image }

func TestAnchorPixelsRGBAEqualsGeneric(t *testing.T) {
	img := image.NewRGBA(image.Rect(7, 11, 103, 65))
	for y := 11; y < 65; y++ {
		for x := 7; x < 103; x++ {
			img.SetRGBA(x, y, color.RGBA{uint8(x), uint8(y), uint8(x + y), 255})
		}
	}
	a := Anchor{X: 4, Y: 5, Width: 68, Height: 40}
	fast, generic := AnchorPixels(img, a, 96, 54), AnchorPixels(genericImage{img}, a, 96, 54)
	for i := range fast {
		if fast[i] != generic[i] {
			t.Fatalf("cell %d differs: %v vs %v", i, fast[i], generic[i])
		}
	}
}

func TestAnchorPixelsNRGBAEqualsGeneric(t *testing.T) {
	img := image.NewNRGBA(image.Rect(7, 11, 103, 65))
	for y := 11; y < 65; y++ {
		for x := 7; x < 103; x++ {
			img.SetNRGBA(x, y, color.NRGBA{uint8(x), uint8(y), uint8(x + y), 255})
		}
	}
	img.SetNRGBA(20, 20, color.NRGBA{200, 100, 50, 100})
	a := Anchor{X: 4, Y: 5, Width: 68, Height: 40}
	fast, generic := AnchorPixels(img, a, 96, 54), AnchorPixels(genericImage{img}, a, 96, 54)
	for i := range fast {
		if fast[i] != generic[i] {
			t.Fatalf("cell %d differs: %v vs %v", i, fast[i], generic[i])
		}
	}
}

func TestPhaseVariantsRequireCompleteMatchingVariant(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 32, 16))
	a := Anchor{X: 0, Y: 0, Width: 16, Height: 8, MaxError: .01}
	b := Anchor{X: 16, Y: 8, Width: 16, Height: 8, MaxError: .01}
	a.Pixels, b.Pixels = AnchorPixels(img, a, 32, 16), AnchorPixels(img, b, 32, 16)
	white := image.NewRGBA(img.Bounds())
	for y := 0; y < 16; y++ {
		for x := 0; x < 32; x++ {
			white.SetRGBA(x, y, color.RGBA{255, 255, 255, 255})
		}
	}
	aw, bw := a, b
	aw.Pixels, bw.Pixels = AnchorPixels(white, aw, 32, 16), AnchorPixels(white, bw, 32, 16)
	m := PhaseModel{Width: 32, Height: 16, Phases: []Phase{{Name: "turn", Anchors: []Anchor{a, b}, Variants: [][]Anchor{{aw, bw}}}}}
	if err := m.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, frame := range []image.Image{img, white} {
		if got, _ := m.Recognize(frame); got != "turn" {
			t.Fatalf("got %s", got)
		}
	}
	mixed := image.NewRGBA(img.Bounds())
	for y := 8; y < 16; y++ {
		for x := 16; x < 32; x++ {
			mixed.SetRGBA(x, y, color.RGBA{255, 255, 255, 255})
		}
	}
	if got, _ := m.Recognize(mixed); got != "unknown" {
		t.Fatalf("mixed variants matched: %s", got)
	}
	m.Phases = append(m.Phases, Phase{Name: "other", Anchors: []Anchor{a, b}})
	if got, _ := m.Recognize(img); got != "unknown" {
		t.Fatalf("ambiguous phases matched: %s", got)
	}
	m.Phases[0].Variants = [][]Anchor{{a}}
	if err := m.Validate(); err == nil {
		t.Fatal("accepted incomplete variant")
	}
}

func TestBuildPhasesGroupsSamples(t *testing.T) {
	dir := t.TempDir()
	img := image.NewRGBA(image.Rect(0, 0, 32, 16))
	f, err := os.Create(filepath.Join(dir, "sample.png"))
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	labels := PhaseLabels{Width: 32, Height: 16}
	for i := 0; i < 2; i++ {
		labels.Samples = append(labels.Samples, struct {
			Name, Image string
			Anchors     []Anchor
		}{"turn", "sample.png", []Anchor{{X: 0, Y: 0, Width: 16, Height: 8, MaxError: .01}, {X: 16, Y: 8, Width: 16, Height: 8, MaxError: .01}}})
	}
	labels.Samples[1].Anchors[1] = Anchor{X: 16, Y: 8, Width: 16, Height: 8, Feature: "green-coverage-v1", MinCoverage: .1, MaxCoverage: .9}
	data, err := json.Marshal(labels)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "labels.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	modelPath := filepath.Join(dir, "model.json")
	if err := BuildPhases(path, modelPath); err != nil {
		t.Fatal(err)
	}
	model, err := LoadPhases(modelPath)
	if err != nil || len(model.Phases) != 1 || len(model.Phases[0].Variants) != 1 {
		t.Fatalf("model=%+v err=%v", model, err)
	}
	if a := model.Phases[0].Variants[0][1]; a.Feature != "green-coverage-v1" || len(a.Pixels) != 0 {
		t.Fatalf("coverage anchor not preserved: %+v", a)
	}
}

func BenchmarkAnchorPixels(b *testing.B) {
	img := image.NewRGBA(image.Rect(0, 0, 1920, 1080))
	a := Anchor{X: 100, Y: 100, Width: 500, Height: 200}
	for _, tc := range []struct {
		name string
		img  image.Image
	}{{"rgba", img}, {"generic", genericImage{img}}} {
		b.Run(tc.name, func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				AnchorPixels(tc.img, a, 1920, 1080)
			}
		})
	}
}
