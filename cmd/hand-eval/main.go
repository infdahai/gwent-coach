// Command hand-eval compares a human annotation with one existing hand-scan record.
package main

import (
	"encoding/json"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"os"

	"gwentcoachmvp/internal/cardmatch"
	"gwentcoachmvp/internal/observe"
)

type label struct {
	Art    cardmatch.ROI `json:"art"`
	CardID int           `json:"card_id"` // zero means the annotator does not know
}
type annotation struct {
	Report string  `json:"report"`
	Image  string  `json:"image"`
	Width  int     `json:"width"`
	Height int     `json:"height"`
	Cards  []label `json:"cards"`
}
type scan struct {
	Status  string                  `json:"status"`
	Reports [2]string               `json:"reports"`
	Cards   []observe.HandCandidate `json:"cards"`
}
type summary struct {
	Trust             string `json:"trust"`
	Labels            int    `json:"labels"`
	Regions           int    `json:"regions"`
	Matched           int    `json:"matched_regions"`
	UnmatchedLabels   int    `json:"unmatched_labels"`
	UnmatchedRegions  int    `json:"unmatched_regions"`
	Correct           int    `json:"identity_agrees"`
	Incorrect         int    `json:"identity_disagrees"`
	UnknownPrediction int    `json:"identity_unknown_prediction"`
	UnknownLabel      int    `json:"identity_unknown_label"`
}

func readJSON(path string, v any) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	d := json.NewDecoder(f)
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return err
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return fmt.Errorf("expected one JSON value")
	}
	return nil
}
func valid(r cardmatch.ROI, w, h int) bool {
	return r.X >= 0 && r.Y >= 0 && r.Width > 0 && r.Height > 0 && r.X <= w && r.Y <= h && r.Width <= w-r.X && r.Height <= h-r.Y
}
func iou(a, b cardmatch.ROI) float64 {
	w := max(0, min(a.X+a.Width, b.X+b.Width)-max(a.X, b.X))
	h := max(0, min(a.Y+a.Height, b.Y+b.Height)-max(a.Y, b.Y))
	intersection := float64(w) * float64(h)
	return intersection / (float64(a.Width)*float64(a.Height) + float64(b.Width)*float64(b.Height) - intersection)
}

// Maximize number of matches, then total overlap; each rectangle is used once.
func evaluate(a annotation, s scan) (summary, error) {
	r := summary{Trust: "human_annotations_and_candidates_unverified", Labels: len(a.Cards), Regions: len(s.Cards)}
	if a.Report == "" || a.Image == "" || a.Width <= 0 || a.Height <= 0 || a.Cards == nil || s.Cards == nil || len(a.Cards) > 10 || len(s.Cards) > 10 || s.Status != "unvalidated_candidates_no_input" || (a.Report != s.Reports[0] && a.Report != s.Reports[1]) {
		return r, fmt.Errorf("invalid annotation, scan, or unmatched report reference")
	}
	for _, c := range a.Cards {
		if !valid(c.Art, a.Width, a.Height) || c.CardID < 0 {
			return r, fmt.Errorf("invalid annotated rectangle or card ID")
		}
	}
	for _, c := range s.Cards {
		if !valid(c.Art, a.Width, a.Height) || (c.Status != "unknown" && c.Status != "candidate") || (c.Status == "candidate" && c.CardID <= 0) || (c.Status == "unknown" && c.CardID != 0) {
			return r, fmt.Errorf("invalid scan rectangle or candidate")
		}
	}
	type state struct {
		n       int
		overlap float64
		pairs   [][2]int
	}
	memo := map[[2]int]state{}
	var match func(int, int) state
	match = func(i, mask int) state {
		if i == len(a.Cards) {
			return state{}
		}
		key := [2]int{i, mask}
		if v, ok := memo[key]; ok {
			return v
		}
		best := match(i+1, mask)
		for j, c := range s.Cards {
			if mask&(1<<j) != 0 {
				continue
			}
			overlap := iou(a.Cards[i].Art, c.Art)
			if overlap < 0.5 {
				continue
			}
			next := match(i+1, mask|1<<j)
			next.n++
			next.overlap += overlap
			next.pairs = append(append([][2]int(nil), next.pairs...), [2]int{i, j})
			if next.n > best.n || (next.n == best.n && next.overlap > best.overlap) {
				best = next
			}
		}
		memo[key] = best
		return best
	}
	pairs := match(0, 0).pairs
	r.Matched = len(pairs)
	r.UnmatchedLabels = r.Labels - r.Matched
	r.UnmatchedRegions = r.Regions - r.Matched
	for _, p := range pairs {
		l, c := a.Cards[p[0]], s.Cards[p[1]]
		switch {
		case l.CardID == 0:
			r.UnknownLabel++
		case c.Status == "unknown":
			r.UnknownPrediction++
		case l.CardID == c.CardID:
			r.Correct++
		default:
			r.Incorrect++
		}
	}
	return r, nil
}
func run(args []string, out io.Writer) error {
	if len(args) != 2 {
		return fmt.Errorf("usage: hand-eval annotation.json scan.json")
	}
	var a annotation
	if err := readJSON(args[0], &a); err != nil {
		return fmt.Errorf("annotation: %w", err)
	}
	// Scan output has timing and per-frame matcher diagnostics which evaluation does not need.
	data, err := os.ReadFile(args[1])
	if err != nil {
		return err
	}
	var s scan
	if err = json.Unmarshal(data, &s); err != nil {
		return fmt.Errorf("scan: %w", err)
	}
	f, err := os.Open(a.Image)
	if err != nil {
		return fmt.Errorf("image: %w", err)
	}
	config, _, err := image.DecodeConfig(f)
	f.Close()
	if err != nil {
		return fmt.Errorf("image: %w", err)
	}
	if config.Width != a.Width || config.Height != a.Height {
		return fmt.Errorf("image dimensions differ from annotation")
	}
	r, err := evaluate(a, s)
	if err != nil {
		return err
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
