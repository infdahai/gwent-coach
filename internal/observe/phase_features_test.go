package observe

import (
	"image"
	"image/color"
	"math"
	"testing"
)

func TestGreenCoverageAndPhaseAmbiguity(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 64, 32))
	for y := 0; y < 16; y++ {
		for x := 0; x < 32; x++ {
			img.SetRGBA(x, y, color.RGBA{20, 180, 40, 255})
		}
	}
	green := Anchor{X: 0, Y: 0, Width: 32, Height: 16, Feature: "green-coverage-v1", MinCoverage: .8, MaxCoverage: 1}
	rgb := Anchor{X: 32, Y: 16, Width: 32, Height: 16, MaxError: .01}
	rgb.Pixels = AnchorPixels(img, rgb, 64, 32)
	m := PhaseModel{Width: 64, Height: 32, Phases: []Phase{{Name: "turn", Anchors: []Anchor{green, rgb}}}}
	if err := m.Validate(); err != nil {
		t.Fatal(err)
	}
	if GreenCoverage(nil, green, 64, 32) != 0 || GreenCoverage(img, green, 0, 32) != 0 {
		t.Fatal("invalid coverage input must not match")
	}
	if got, _ := m.Recognize(img); got != "turn" {
		t.Fatalf("green positive: %s", got)
	}
	blank := image.NewRGBA(img.Bounds())
	if got, _ := m.Recognize(blank); got != "unknown" {
		t.Fatalf("blank matched: %s", got)
	}
	m.Phases = append(m.Phases, Phase{Name: "other", Anchors: []Anchor{green, rgb}})
	if got, _ := m.Recognize(img); got != "unknown" {
		t.Fatalf("ambiguous matched: %s", got)
	}
	diagnostics := phaseAnchorDiagnostics(&m, img)
	if len(diagnostics) != 4 || diagnostics[0].Feature != "green-coverage-v1" || diagnostics[0].Coverage != 1 {
		t.Fatalf("green diagnostics: %+v", diagnostics)
	}
	m.Phases[0].Anchors[0].Mode = "desaturated"
	if err := m.Validate(); err == nil {
		t.Fatal("accepted mixed mode and feature")
	}
	m.Phases[0].Anchors[0].Mode = ""
	m.Phases[0].Anchors[0].MinCoverage = math.NaN()
	if err := m.Validate(); err == nil {
		t.Fatal("accepted NaN coverage")
	}
}

func TestRequiresRGBAnchorEveryVariant(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 64, 32))
	art := Anchor{X: 0, Y: 0, Width: 32, Height: 16, MaxError: .05}
	other := Anchor{X: 32, Y: 16, Width: 32, Height: 16, MaxError: .05}
	art.Pixels, other.Pixels = AnchorPixels(img, art, 64, 32), AnchorPixels(img, other, 64, 32)
	phase := Phase{Name: "placed", Anchors: []Anchor{art, other}, Variants: [][]Anchor{{art, other}}}
	m := PhaseModel{Width: 64, Height: 32, Phases: []Phase{phase}}
	roi := image.Rect(0, 0, 32, 16)
	if !m.RequiresRGBAnchor("placed", roi, .08) {
		t.Fatal("rejected anchor present in all variants")
	}
	m.Phases[0].Variants[0] = []Anchor{other, other}
	if got, _ := m.Recognize(img); got != "placed" {
		t.Fatalf("fixture variant should match: %s", got)
	}
	if m.RequiresRGBAnchor("placed", roi, .08) {
		t.Fatal("accepted variant lacking identity anchor")
	}
	m.Phases[0].Variants[0] = []Anchor{art, other}
	green := Anchor{X: 0, Y: 0, Width: 32, Height: 16, Feature: "green-coverage-v1", MinCoverage: .1, MaxCoverage: 1}
	m.Phases[0].Variants[0] = []Anchor{green, other}
	if m.RequiresRGBAnchor("placed", roi, .08) {
		t.Fatal("accepted non-RGB identity anchor")
	}
	m.Phases[0].Variants[0] = []Anchor{art, other}
	m.Phases[0].Variants[0][0].MaxError = .1
	if m.RequiresRGBAnchor("placed", roi, .08) {
		t.Fatal("accepted looser identity threshold")
	}
	if m.RequiresRGBAnchor("missing", roi, .08) || m.RequiresRGBAnchor("placed", roi, math.NaN()) {
		t.Fatal("accepted invalid identity check")
	}
}
