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

	"gwentcoachmvp/internal/observe"
	"gwentcoachmvp/internal/stateestimate"
)

func TestRunEvaluatesLabelsAndUnknown(t *testing.T) {
	dir := t.TempDir()
	makeImage := func(name string, c color.RGBA) image.Image {
		img := image.NewRGBA(image.Rect(0, 0, 32, 16))
		for y := 0; y < 16; y++ {
			for x := 0; x < 32; x++ {
				img.Set(x, y, c)
			}
		}
		f, err := os.Create(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if err = png.Encode(f, img); err != nil {
			t.Fatal(err)
		}
		f.Close()
		return img
	}
	white := makeImage("present.png", color.RGBA{255, 255, 255, 255})
	makeImage("absent.png", color.RGBA{0, 0, 0, 255})
	a := observe.Anchor{X: 0, Y: 0, Width: 16, Height: 8, MaxError: 0.05}
	a.Pixels = observe.AnchorPixels(white, a, 32, 16)
	b := a
	b.X = 16
	m := observe.PhaseModel{Width: 32, Height: 16, Phases: []observe.Phase{{Name: "target", Anchors: []observe.Anchor{a, b}}}}
	write := func(name string, v any) string {
		t.Helper()
		data, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(dir, name)
		if err = os.WriteFile(p, data, 0600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	cal := observe.EvidenceCalibration{Stage: m, Targets: []*observe.FactCalibration[stateestimate.Target]{{Model: m, Values: map[string]stateestimate.Target{"target": {Kind: "control", ID: "pass", LayoutID: "fixture", Bounds: image.Rect(0, 0, 16, 8)}}, RequiredRGB: []observe.RGBRequirement{{ROI: image.Rect(0, 0, 16, 8), MaxError: 0.05}}}}}
	reader, err := observe.NewCalibratedEvidenceReader(cal)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reader.TargetCandidates(image.NewRGBA(image.Rect(0, 0, 16, 8))); err == nil {
		t.Fatal("non-native image accepted")
	}
	occluded := image.NewRGBA(image.Rect(0, 0, 32, 16))
	for y := 0; y < 16; y++ {
		for x := 16; x < 32; x++ {
			occluded.Set(x, y, color.White)
		}
	}
	if got, err := reader.TargetCandidates(occluded); err != nil || len(got) != 0 {
		t.Fatalf("occluded target accepted: %v %v", got, err)
	}
	bad := cal
	bad.Targets = []*observe.FactCalibration[stateestimate.Target]{{Model: m}}
	if _, err := observe.NewCalibratedEvidenceReader(bad); err == nil {
		t.Fatal("invalid target calibration accepted")
	}
	model := write("model.json", cal)
	labels := write("labels.json", manifest{Samples: []sample{{"present.png", "present"}, {"absent.png", "absent"}, {"present.png", "absent"}}})
	if err := run([]string{model, labels}, &bytes.Buffer{}); err == nil {
		t.Fatal("duplicate image accepted")
	}
	labels = write("labels.json", manifest{Samples: []sample{{"present.png", "present"}, {"absent.png", "absent"}}})
	var out bytes.Buffer
	if err := run([]string{model, labels}, &out); err != nil {
		t.Fatal(err)
	}
	var r report
	if err := json.Unmarshal(out.Bytes(), &r); err != nil {
		t.Fatal(err)
	}
	if r.TruePositive != 1 || r.TrueNegative != 1 || r.Results[1].Prediction != "unknown" {
		t.Fatalf("unexpected report: %+v", r)
	}
	labels = write("labels.json", manifest{Samples: []sample{{"present.png", "absent"}, {"absent.png", "present"}}})
	out.Reset()
	if err := run([]string{model, labels}, &out); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(out.Bytes(), &r); err != nil {
		t.Fatal(err)
	}
	if r.FalsePositive != 1 || r.FalseNegative != 1 {
		t.Fatalf("unexpected errors: %+v", r)
	}
	labels = write("labels.json", manifest{Samples: []sample{{"present.png", "unknown"}}})
	if err := run([]string{model, labels}, &bytes.Buffer{}); err == nil {
		t.Fatal("invalid label accepted")
	}
}
