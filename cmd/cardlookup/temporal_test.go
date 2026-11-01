package main

import (
	"bytes"
	"encoding/json"
	"gwentcoachmvp/internal/cardmatch"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTemporalEvaluation(t *testing.T) {
	dir := t.TempDir()
	im := image.NewRGBA(image.Rect(0, 0, 30, 10))
	for y := 0; y < 10; y++ {
		for x := 0; x < 30; x++ {
			v := uint8(20 + x/10*90)
			im.Set(x, y, color.RGBA{v, v, v, 255})
		}
	}
	f, e := os.Create(filepath.Join(dir, "frame.png"))
	if e != nil {
		t.Fatal(e)
	}
	if e = png.Encode(f, im); e != nil {
		t.Fatal(e)
	}
	f.Close()
	regions := []temporalAnnotation{{ROI: cardmatch.ROI{0, 0, 10, 10}, Visible: true, Instance: "a"}, {ROI: cardmatch.ROI{10, 0, 10, 10}, Visible: true, Instance: "b"}, {ROI: cardmatch.ROI{20, 0, 10, 10}, Visible: false, Instance: "c"}}
	now := time.Now().UTC()
	m := temporalManifest{MaxGapMS: 2000, MaxDistance: 2, MinGap: 10, Frames: []temporalFrame{{"frame.png", "one", now, regions}, {"frame.png", "one", now.Add(time.Second), regions}, {"frame.png", "two", now.Add(2 * time.Second), regions}}}
	write := func() {
		b, _ := json.Marshal(m)
		if e = os.WriteFile(filepath.Join(dir, "m.json"), b, 0600); e != nil {
			t.Fatal(e)
		}
	}
	write()
	var out bytes.Buffer
	if e = evaluateTemporal(filepath.Join(dir, "m.json"), &out); e != nil {
		t.Fatal(e)
	}
	var counts map[string]json.RawMessage
	if e = json.Unmarshal(out.Bytes(), &counts); e != nil {
		t.Fatal(e)
	}
	if string(counts["correct"]) != "2" || string(counts["unmatched_current_regions"]) != "4" || string(counts["skipped_pairs"]) != "1" {
		t.Fatal(out.String())
	}
	m.Frames[1].Regions = append([]temporalAnnotation(nil), regions...)
	m.Frames[1].Regions[1].Instance = "a"
	write()
	out.Reset()
	if e = evaluateTemporal(filepath.Join(dir, "m.json"), &out); e == nil {
		t.Fatal("duplicate instance accepted")
	}
}
