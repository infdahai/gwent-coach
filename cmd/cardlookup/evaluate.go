package main

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"

	"gwentcoachmvp/internal/cardindex"
	"gwentcoachmvp/internal/cardmatch"
)

// evaluateSamples compares independent, manually labelled image regions with an
// existing template index. A zero card ID means the region must be rejected
// (for example, an occlusion or a card outside the template catalog).
func evaluateSamples(manifestPath, templatePath string, db *cardindex.Index, thresholds cardmatch.Thresholds, out io.Writer) error {
	if err := thresholds.Validate(); err != nil {
		return err
	}
	var m manifest
	if err := cardindex.ReadJSON(manifestPath, &m); err != nil {
		return err
	}
	if m.DataVersion != db.Version().Data || m.RulesVersion != db.Version().Rules || len(m.Samples) == 0 {
		return fmt.Errorf("nonempty evaluation manifest with matching versions required")
	}
	var file cardmatch.File
	if err := cardindex.ReadJSON(templatePath, &file); err != nil {
		return err
	}
	index, err := cardmatch.New(file, db)
	if err != nil {
		return err
	}
	type result struct {
		Sample  int              `json:"sample"`
		Label   int              `json:"label"`
		Outcome string           `json:"outcome"`
		Match   cardmatch.Result `json:"match"`
	}
	report := struct {
		Trust           string   `json:"trust"`
		Correct         int      `json:"correct"`
		FalseMatch      int      `json:"false_match"`
		RejectedKnown   int      `json:"rejected_known"`
		RejectedUnknown int      `json:"rejected_unknown"`
		Results         []result `json:"results"`
	}{Trust: "offline_unvalidated_no_input; labels and sample independence are not verified", Results: make([]result, 0, len(m.Samples))}
	for n, s := range m.Samples {
		if s.CardID < 0 {
			return fmt.Errorf("sample %d: invalid label", n)
		}
		if s.CardID > 0 {
			if _, _, _, ok := db.Lookup(s.CardID, "en"); !ok {
				return fmt.Errorf("sample %d: unknown label %d", n, s.CardID)
			}
		}
		path := s.Image
		if !filepath.IsAbs(path) {
			path = filepath.Join(filepath.Dir(manifestPath), path)
		}
		im, err := readImage(path)
		if err != nil {
			return fmt.Errorf("sample %d: %w", n, err)
		}
		feature, err := cardmatch.Normalize(im, s.ROI)
		var match cardmatch.Result
		if err == cardmatch.ErrDegenerateArt {
			match = cardmatch.Result{Status: "unknown", BestDistance: -1, RunnerDistance: -1, Calibration: "unvalidated", Reason: "degenerate_art"}
		} else if err != nil {
			return fmt.Errorf("sample %d: %w", n, err)
		} else {
			match, err = index.Match(feature, nil, thresholds)
			if err != nil {
				return fmt.Errorf("sample %d: %w", n, err)
			}
		}
		if match.Status == "candidate" && match.RunnerDistance < 0 {
			match.Status, match.CardID, match.Reason = "unknown", 0, "no_competing_identity"
		}
		outcome := ""
		switch {
		case match.Status == "unknown" && s.CardID == 0:
			outcome = "rejected_unknown"
			report.RejectedUnknown++
		case match.Status == "unknown":
			outcome = "rejected_known"
			report.RejectedKnown++
		case s.CardID == match.CardID && match.RunnerDistance >= 0:
			outcome = "correct"
			report.Correct++
		default:
			outcome = "false_match"
			report.FalseMatch++
		}
		report.Results = append(report.Results, result{n, s.CardID, outcome, match})
	}
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(report)
}
