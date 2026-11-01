// Command target-eval compares a single target model against labelled offline images.
package main

import (
	"encoding/json"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"os"
	"path/filepath"

	"gwentcoachmvp/internal/observe"
)

type sample struct {
	Image string `json:"image"`
	Label string `json:"label"`
}

type manifest struct {
	Samples []sample `json:"samples"`
}

type result struct {
	Image      string `json:"image"`
	Label      string `json:"label"`
	Prediction string `json:"prediction"`
}

type report struct {
	Status        string   `json:"status"`
	Results       []result `json:"results"`
	TruePositive  int      `json:"true_positive"`
	FalsePositive int      `json:"false_positive"`
	FalseNegative int      `json:"false_negative"`
	TrueNegative  int      `json:"true_negative"`
}

func run(args []string, out io.Writer) error {
	if len(args) != 2 {
		return fmt.Errorf("usage: target-eval model.json labels.json")
	}
	data, err := os.ReadFile(args[0])
	if err != nil {
		return err
	}
	var cal observe.EvidenceCalibration
	if err = json.Unmarshal(data, &cal); err != nil {
		return fmt.Errorf("calibration: %w", err)
	}
	if len(cal.Targets) != 1 || cal.Targets[0] == nil || len(cal.Targets[0].Model.Phases) != 1 {
		return fmt.Errorf("calibration must contain one target with one phase")
	}
	reader, err := observe.NewCalibratedEvidenceReader(cal)
	if err != nil {
		return fmt.Errorf("calibration: %w", err)
	}
	f, err := os.Open(args[1])
	if err != nil {
		return err
	}
	defer f.Close()
	dec := json.NewDecoder(f)
	dec.DisallowUnknownFields()
	var labels manifest
	if err := dec.Decode(&labels); err != nil {
		return fmt.Errorf("labels: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return fmt.Errorf("labels must contain one JSON value")
	}
	if len(labels.Samples) == 0 {
		return fmt.Errorf("labels require samples")
	}
	r := report{Status: "unvalidated_candidates_no_input", Results: make([]result, 0, len(labels.Samples))}
	seen := map[string]bool{}
	for _, s := range labels.Samples {
		if s.Image == "" || (s.Label != "present" && s.Label != "absent") {
			return fmt.Errorf("each sample requires an image and present or absent label")
		}
		path := s.Image
		if !filepath.IsAbs(path) {
			path = filepath.Join(filepath.Dir(args[1]), path)
		}
		path, err = filepath.Abs(path)
		if err != nil {
			return err
		}
		if seen[path] {
			return fmt.Errorf("duplicate image: %s", s.Image)
		}
		seen[path] = true
		imgFile, err := os.Open(path)
		if err != nil {
			return fmt.Errorf("image %s: %w", s.Image, err)
		}
		img, _, err := image.Decode(imgFile)
		imgFile.Close()
		if err != nil {
			return fmt.Errorf("image %s: %w", s.Image, err)
		}
		candidates, err := reader.TargetCandidates(img)
		if err != nil {
			return fmt.Errorf("image %s: %w", s.Image, err)
		}
		prediction := "unknown"
		if len(candidates) != 0 {
			prediction = "present"
		}
		r.Results = append(r.Results, result{s.Image, s.Label, prediction})
		switch {
		case s.Label == "present" && prediction == "present":
			r.TruePositive++
		case s.Label == "absent" && prediction == "present":
			r.FalsePositive++
		case s.Label == "present":
			r.FalseNegative++
		default:
			r.TrueNegative++
		}
	}
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(r)
}

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
