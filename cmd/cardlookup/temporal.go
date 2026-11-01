package main

import (
	"encoding/json"
	"fmt"
	"image"
	"io"
	"os"
	"path/filepath"
	"time"

	"gwentcoachmvp/internal/cardmatch"
)

type temporalAnnotation struct {
	ROI      cardmatch.ROI `json:"roi"`
	Visible  bool          `json:"visible"`
	Instance string        `json:"instance"`
}
type temporalFrame struct {
	Image      string               `json:"image"`
	Batch      string               `json:"batch"`
	CapturedAt time.Time            `json:"captured_at"`
	Regions    []temporalAnnotation `json:"regions"`
	Complete   bool                 `json:"complete"`
}
type temporalManifest struct {
	MaxGapMS    int             `json:"max_gap_ms"`
	MaxDistance int             `json:"max_distance"`
	MinGap      int             `json:"min_gap"`
	Frames      []temporalFrame `json:"frames"`
}
type temporalPair struct {
	Previous int                      `json:"previous_frame"`
	Current  int                      `json:"current_frame"`
	Status   string                   `json:"status"`
	Links    []cardmatch.TemporalLink `json:"links"`
}
type imageFrame struct {
	frame   temporalFrame
	image   image.Image
	regions []cardmatch.TemporalRegion
}

// evaluateTemporal evaluates human-provided instance labels, without deriving
// identities or authorizing game input. Empty labels are unscored.
func evaluateTemporal(path string, out io.Writer) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	dec := json.NewDecoder(f)
	dec.DisallowUnknownFields()
	var m temporalManifest
	if err = dec.Decode(&m); err != nil {
		return err
	}
	var extra any
	if err = dec.Decode(&extra); err != io.EOF {
		return fmt.Errorf("expected one manifest")
	}
	if len(m.Frames) < 2 || m.MaxGapMS <= 0 || m.MaxDistance < 0 || m.MaxDistance > 255 || m.MinGap < 1 || m.MinGap > 255 {
		return fmt.Errorf("invalid frames or thresholds")
	}
	type report struct {
		Trust     string         `json:"trust"`
		Correct   int            `json:"correct"`
		Incorrect int            `json:"incorrect"`
		Unscored  int            `json:"unscored_links"`
		Unmatched int            `json:"unmatched_current_regions"`
		Skipped   int            `json:"skipped_pairs"`
		Occluded  int            `json:"labelled_became_occluded"`
		Missing   int            `json:"labelled_absent_from_complete_frame"`
		Missed    int            `json:"labelled_visible_not_correctly_linked"`
		Pairs     []temporalPair `json:"pairs"`
	}
	r := report{Trust: "offline_unvalidated_no_input; instance labels and visibility are human supplied", Pairs: []temporalPair{}}
	var previous imageFrame
	for i, frame := range m.Frames {
		if frame.Image == "" || frame.Batch == "" || frame.CapturedAt.IsZero() || len(frame.Regions) == 0 || len(frame.Regions) > 10 {
			return fmt.Errorf("frame %d: invalid metadata or regions", i)
		}
		seen := map[string]bool{}
		regions := make([]cardmatch.TemporalRegion, 0, len(frame.Regions))
		for _, a := range frame.Regions {
			if a.Instance != "" {
				if seen[a.Instance] {
					return fmt.Errorf("frame %d: duplicate instance annotation", i)
				}
				seen[a.Instance] = true
			}
			regions = append(regions, cardmatch.TemporalRegion{ROI: a.ROI, Visible: a.Visible})
		}
		p := frame.Image
		if !filepath.IsAbs(p) {
			p = filepath.Join(filepath.Dir(path), p)
		}
		im, err := readImage(p)
		if err != nil {
			return fmt.Errorf("frame %d: %w", i, err)
		}
		if i > 0 {
			if !frame.CapturedAt.After(previous.frame.CapturedAt) {
				return fmt.Errorf("frame %d: timestamps not strictly increasing", i)
			}
			pair := temporalPair{Previous: i - 1, Current: i, Links: []cardmatch.TemporalLink{}}
			if frame.Batch != previous.frame.Batch || frame.CapturedAt.Sub(previous.frame.CapturedAt) > time.Duration(m.MaxGapMS)*time.Millisecond {
				pair.Status = "discontinuity"
				r.Skipped++
				r.Unmatched += len(regions)
			} else {
				links, err := cardmatch.LinkRegions(previous.image, im, previous.frame.Batch, frame.Batch, previous.frame.CapturedAt, frame.CapturedAt, time.Duration(m.MaxGapMS)*time.Millisecond, previous.regions, regions, m.MaxDistance, m.MinGap)
				if err != nil {
					return fmt.Errorf("frame %d: %w", i, err)
				}
				pair.Status = "compared"
				pair.Links = links
				r.Unmatched += len(regions) - len(links)
				current := make(map[string]temporalAnnotation)
				for _, a := range frame.Regions {
					if a.Instance != "" {
						current[a.Instance] = a
					}
				}
				correctLinks := make(map[string]bool)
				for _, link := range links {
					a, b := previous.frame.Regions[link.Previous].Instance, frame.Regions[link.Current].Instance
					if a != "" && a == b {
						correctLinks[a] = true
					}
				}
				for _, a := range previous.frame.Regions {
					if a.Instance == "" || !a.Visible {
						continue
					}
					b, exists := current[a.Instance]
					switch {
					case exists && !b.Visible:
						r.Occluded++
					case !exists && frame.Complete:
						r.Missing++
					case exists && b.Visible && !correctLinks[a.Instance]:
						r.Missed++
					}
				}
				for _, link := range links {
					a, b := previous.frame.Regions[link.Previous].Instance, frame.Regions[link.Current].Instance
					if a == "" || b == "" {
						r.Unscored++
					} else if a == b {
						r.Correct++
					} else {
						r.Incorrect++
					}
				}
			}
			r.Pairs = append(r.Pairs, pair)
		}
		previous = imageFrame{frame: frame, image: im, regions: regions}
	}
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(r)
}
