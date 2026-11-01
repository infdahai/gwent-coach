// Package cardmatch produces uncalibrated identity candidates from explicit art ROIs.
// It does not locate cards, map input coordinates, or establish live-game accuracy.
package cardmatch

import (
	"errors"
	"fmt"
	"image"
	"math/bits"
	"sort"

	"gwentcoachmvp/internal/cardindex"
)

const FeatureVersion = "gray-area-dhash-9x8-v1"

type ROI struct {
	X      int `json:"x"`
	Y      int `json:"y"`
	Width  int `json:"width"`
	Height int `json:"height"`
}

type Feature struct {
	Gray [72]uint8 `json:"gray"`
}

func (f Feature) hash() uint64 {
	var hash uint64
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			if f.Gray[y*9+x] > f.Gray[y*9+x+1] {
				hash |= 1 << uint(y*8+x)
			}
		}
	}
	return hash
}

func (f Feature) valid() bool {
	lo, hi := uint8(255), uint8(0)
	for _, g := range f.Gray {
		if g < lo {
			lo = g
		}
		if g > hi {
			hi = g
		}
	}
	return hi-lo >= 8
}

// Normalize area-averages the caller's art rectangle into 9x8 grayscale cells.
// Coordinates are in the original image; no implicit crop or coordinate transform occurs.
var ErrDegenerateArt = errors.New("blank or degenerate normalized art")

func Normalize(img image.Image, roi ROI) (Feature, error) {
	var f Feature
	if img == nil {
		return f, fmt.Errorf("nil image")
	}
	b := img.Bounds()
	if roi.Width < 9 || roi.Height < 8 || roi.X < b.Min.X || roi.Y < b.Min.Y || roi.X > b.Max.X || roi.Y > b.Max.Y || roi.Width > b.Max.X-roi.X || roi.Height > b.Max.Y-roi.Y {
		return f, fmt.Errorf("invalid art ROI: must be inside image and at least 9x8")
	}
	for y := 0; y < 8; y++ {
		for x := 0; x < 9; x++ {
			x0, x1 := roi.X+x*roi.Width/9, roi.X+(x+1)*roi.Width/9
			y0, y1 := roi.Y+y*roi.Height/8, roi.Y+(y+1)*roi.Height/8
			var sum, count uint64
			for py := y0; py < y1; py++ {
				for px := x0; px < x1; px++ {
					var r, g, b, a uint32
					switch src := img.(type) {
					case *image.RGBA:
						i := src.PixOffset(px, py)
						r, g, b, a = uint32(src.Pix[i])*257, uint32(src.Pix[i+1])*257, uint32(src.Pix[i+2])*257, uint32(src.Pix[i+3])*257
					case *image.NRGBA:
						i := src.PixOffset(px, py)
						a = uint32(src.Pix[i+3]) * 257
						if a == 65535 {
							r, g, b = uint32(src.Pix[i])*257, uint32(src.Pix[i+1])*257, uint32(src.Pix[i+2])*257
						}
					default:
						r, g, b, a = img.At(px, py).RGBA()
					}
					if a != 65535 {
						return f, fmt.Errorf("art ROI contains transparency")
					}
					sum += uint64((19595*r + 38470*g + 7471*b + 32768) >> 24)
					count++
				}
			}
			f.Gray[y*9+x] = uint8(sum / count)
		}
	}
	if !f.valid() {
		return Feature{}, ErrDegenerateArt
	}
	return f, nil
}

type Template struct {
	CardID  int     `json:"card_id"`
	Feature Feature `json:"feature"`
}

type File struct {
	Version        cardindex.Version `json:"version"`
	FeatureVersion string            `json:"feature_version"`
	Templates      []Template        `json:"templates"`
}

type entry struct{ hash uint64 }
type Index struct{ byID map[int][]entry }

// New validates all IDs against the versioned effect database. Multiple templates
// per ID are allowed; shared art across different IDs stays ambiguous.
func New(file File, db *cardindex.Index) (*Index, error) {
	if db == nil || file.Version != db.Version() || file.FeatureVersion != FeatureVersion {
		return nil, fmt.Errorf("template version mismatch")
	}
	if len(file.Templates) == 0 {
		return nil, fmt.Errorf("empty template index")
	}
	i := &Index{byID: map[int][]entry{}}
	for _, t := range file.Templates {
		if _, _, _, ok := db.Lookup(t.CardID, "en"); !ok {
			return nil, fmt.Errorf("unknown template card %d", t.CardID)
		}
		if !t.Feature.valid() {
			return nil, fmt.Errorf("degenerate template for %d", t.CardID)
		}
		i.byID[t.CardID] = append(i.byID[t.CardID], entry{t.Feature.hash()})
	}
	return i, nil
}

// Thresholds must be explicitly supplied. They are not a calibration certificate.
type Thresholds struct {
	MaxDistance int
	MinGap      int
}

func (t Thresholds) Validate() error {
	if t.MaxDistance < 0 || t.MaxDistance > 64 || t.MinGap < 1 || t.MinGap > 64 {
		return fmt.Errorf("explicit max distance 0..64 and minimum gap 1..64 required")
	}
	return nil
}

type Result struct {
	Status         string `json:"status"`
	CardID         int    `json:"card_id,omitempty"`
	BestDistance   int    `json:"best_distance"`
	RunnerDistance int    `json:"runner_distance"`
	Candidates     int    `json:"candidate_ids"`
	Calibration    string `json:"calibration"`
	Reason         string `json:"reason"`
}

// Match returns candidate or unknown, never a trusted identity. nil deck searches
// all templates; an explicitly empty deck is an error. Runner-up is a distinct ID.
func (i *Index) Match(f Feature, deck []int, thresholds Thresholds) (Result, error) {
	r := Result{Status: "unknown", BestDistance: -1, RunnerDistance: -1, Calibration: "unvalidated"}
	if err := thresholds.Validate(); err != nil {
		return r, err
	}
	if !f.valid() {
		return r, fmt.Errorf("degenerate query feature")
	}
	ids := deck
	if deck == nil {
		for id := range i.byID {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return r, fmt.Errorf("empty candidate set")
	}
	// Sorting keeps diagnostic best IDs stable on ties; ties still reject.
	ids = append([]int(nil), ids...)
	sort.Ints(ids)
	hash := f.hash()
	best, runner, bestID := 65, 65, 0
	last := -1
	for _, id := range ids {
		if id == last {
			continue
		}
		last = id
		templates, ok := i.byID[id]
		if !ok {
			return r, fmt.Errorf("no templates for candidate %d", id)
		}
		distance := 65
		for _, t := range templates {
			d := bits.OnesCount64(hash ^ t.hash)
			if d < distance {
				distance = d
			}
		}
		r.Candidates++
		if distance < best {
			runner, best, bestID = best, distance, id
		} else if distance < runner {
			runner = distance
		}
	}
	r.BestDistance = best
	if runner <= 64 {
		r.RunnerDistance = runner
	}
	if best > thresholds.MaxDistance {
		r.Reason = "absolute_distance"
		return r, nil
	}
	if runner <= 64 && runner-best < thresholds.MinGap {
		r.Reason = "ambiguous"
		return r, nil
	}
	r.Status, r.CardID, r.Reason = "candidate", bestID, "within_explicit_thresholds"
	return r, nil
}
