package main

import (
	"bytes"
	"encoding/json"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"gwentcoachmvp/internal/cardmatch"
	"gwentcoachmvp/internal/observe"
)

func TestOfflineEvaluation(t *testing.T) {
	dir := t.TempDir()
	img := filepath.Join(dir, "synthetic.png")
	f, err := os.Create(img)
	if err != nil {
		t.Fatal(err)
	}
	if err = png.Encode(f, image.NewGray(image.Rect(0, 0, 100, 100))); err != nil {
		t.Fatal(err)
	}
	f.Close()
	rect := func(x int) cardmatch.ROI { return cardmatch.ROI{X: x, Y: 10, Width: 10, Height: 20} }
	a := annotation{Report: "report-1", Image: img, Width: 100, Height: 100, Cards: []label{{rect(0), 1}, {rect(20), 2}, {rect(40), 3}, {rect(60), 0}}}
	s := scan{Status: "unvalidated_candidates_no_input", Reports: [2]string{"report-1", "report-2"}, Cards: []observe.HandCandidate{{Art: rect(0), CardID: 1, Status: "candidate"}, {Art: rect(20), CardID: 9, Status: "candidate"}, {Art: rect(40), Status: "unknown"}, {Art: rect(60), CardID: 4, Status: "candidate"}, {Art: rect(80), Status: "unknown"}}}
	write := func(name string, v any) string {
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
	var out bytes.Buffer
	annotationPath := write("a.json", a)
	if err := run([]string{annotationPath, write("s.json", s)}, &out); err != nil {
		t.Fatal(err)
	}
	var got summary
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Matched != 4 || got.UnmatchedRegions != 1 || got.Correct != 1 || got.Incorrect != 1 || got.UnknownPrediction != 1 || got.UnknownLabel != 1 {
		t.Fatalf("%+v", got)
	}
	out.Reset()
	regions := regionReport{Report: "report-1", Regions: []cardmatch.ROI{rect(0), rect(40), rect(80)}}
	if err := run([]string{"--regions", annotationPath, write("regions.json", regions)}, &out); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Matched != 2 || got.UnmatchedLabels != 2 || got.UnmatchedRegions != 1 || got.UnknownPrediction != 2 {
		t.Fatalf("regions: %+v", got)
	}
	regions.Report = "other"
	if err := run([]string{"--regions", annotationPath, write("wrong.json", regions)}, &out); err == nil {
		t.Fatal("accepted mismatched region report")
	}
	regions.Report = "report-1"
	regions.Regions = nil
	if err := run([]string{"--regions", annotationPath, write("null.json", regions)}, &out); err == nil {
		t.Fatal("accepted missing regions")
	}
	a.Report = "wrong"
	if _, err := evaluate(a, s); err == nil {
		t.Fatal("accepted wrong report")
	}
	a.Report = "report-1"
	a.Cards[0].Art.Width = 101
	if _, err := evaluate(a, s); err == nil {
		t.Fatal("accepted out-of-bounds annotation")
	}
}

func TestMatchingMaximizesCount(t *testing.T) {
	a := annotation{Report: "r", Image: "synthetic", Width: 100, Height: 100, Cards: []label{{cardmatch.ROI{X: 4, Y: 0, Width: 10, Height: 10}, 1}, {cardmatch.ROI{X: 0, Y: 0, Width: 10, Height: 10}, 2}}}
	s := scan{Status: "unvalidated_candidates_no_input", Reports: [2]string{"r", "s"}, Cards: []observe.HandCandidate{{Art: cardmatch.ROI{X: 1, Y: 0, Width: 10, Height: 10}, Status: "unknown"}, {Art: cardmatch.ROI{X: 7, Y: 0, Width: 10, Height: 10}, Status: "unknown"}}}
	got, err := evaluate(a, s)
	if err != nil || got.Matched != 2 {
		t.Fatalf("%+v %v", got, err)
	}
}
