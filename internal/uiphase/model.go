package uiphase

import (
	"fmt"
	"image"
	"math"
)

// PhaseModel uses operator-labelled, local-only UI crops. No account images are embedded.
type PhaseModel struct {
	Width, Height int
	Phases        []Phase
}
type Phase struct {
	Name     string
	Anchors  []Anchor
	Variants [][]Anchor `json:",omitempty"`
}
type Anchor struct {
	X, Y, Width, Height      int
	Pixels                   []float64
	MaxError                 float64
	Mode                     string  `json:",omitempty"`
	Feature                  string  `json:",omitempty"`
	MinCoverage, MaxCoverage float64 `json:",omitempty"`
}
type PhaseScore struct {
	Name  string
	Error float64
}

func (m *PhaseModel) Validate() error {
	if m.Width < 1 || m.Height < 1 || len(m.Phases) == 0 {
		return fmt.Errorf("empty phase model")
	}
	seen := map[string]bool{}
	for _, p := range m.Phases {
		if p.Name == "" || p.Name == "unknown" || seen[p.Name] || len(p.Anchors) < 2 {
			return fmt.Errorf("phase requires unique name and at least two anchors")
		}
		seen[p.Name] = true
		sets := append([][]Anchor{p.Anchors}, p.Variants...)
		for _, anchors := range sets {
			if len(anchors) < 2 {
				return fmt.Errorf("phase variant requires at least two anchors")
			}
			for _, a := range anchors {
				if a.X < 0 || a.Y < 0 || a.Width < 1 || a.Height < 1 || a.X > m.Width || a.Y > m.Height || a.Width > m.Width-a.X || a.Height > m.Height-a.Y {
					return fmt.Errorf("invalid anchor in %s", p.Name)
				}
				if a.Feature == "green-coverage-v1" {
					if a.Mode != "" || len(a.Pixels) != 0 || a.MaxError != 0 || math.IsNaN(a.MinCoverage) || math.IsNaN(a.MaxCoverage) || a.MinCoverage <= 0 || a.MaxCoverage > 1 || a.MinCoverage >= a.MaxCoverage {
						return fmt.Errorf("invalid green coverage anchor in %s", p.Name)
					}
					continue
				}
				if a.Feature != "" || a.MinCoverage != 0 || a.MaxCoverage != 0 {
					return fmt.Errorf("invalid anchor feature in %s", p.Name)
				}
				maxError := 0.15
				if a.Mode == "desaturated" {
					maxError = 0.5
				} else if a.Mode == "saturated" {
					maxError = 0.99
				} else if a.Mode != "" {
					return fmt.Errorf("invalid anchor mode in %s", p.Name)
				}
				if a.X < 0 || a.Y < 0 || a.Width < 1 || a.Height < 1 || a.X+a.Width > m.Width || a.Y+a.Height > m.Height || len(a.Pixels) != 16*8*3 || a.MaxError <= 0 || a.MaxError > maxError || math.IsNaN(a.MaxError) {
					return fmt.Errorf("invalid anchor in %s", p.Name)
				}
				for _, v := range a.Pixels {
					if v < 0 || v > 1 || math.IsNaN(v) || math.IsInf(v, 0) {
						return fmt.Errorf("invalid pixel")
					}
				}
			}
		}
	}
	return nil
}

// RequiresRGBAnchor ensures every alternative requires the bound art region.
func (m *PhaseModel) RequiresRGBAnchor(phase string, roi image.Rectangle, maxError float64) bool {
	if m == nil || roi.Empty() || math.IsNaN(maxError) || maxError <= 0 || maxError > 0.15 || m.Validate() != nil {
		return false
	}
	for _, p := range m.Phases {
		if p.Name != phase {
			continue
		}
		for _, anchors := range append([][]Anchor{p.Anchors}, p.Variants...) {
			found := false
			for _, a := range anchors {
				if a.Feature == "" && a.Mode == "" && a.X == roi.Min.X && a.Y == roi.Min.Y && a.Width == roi.Dx() && a.Height == roi.Dy() && a.MaxError > 0 && a.MaxError <= maxError {
					found = true
					break
				}
			}
			if !found {
				return false
			}
		}
		return true
	}
	return false
}

// AnchorPixels averages cells rather than comparing animated full frames exactly.
func AnchorPixels(img image.Image, a Anchor, w, h int) []float64 {
	b := img.Bounds()
	out := make([]float64, 16*8*3)
	for gy := 0; gy < 8; gy++ {
		for gx := 0; gx < 16; gx++ {
			x0 := b.Min.X + (a.X+a.Width*gx/16)*b.Dx()/w
			x1 := b.Min.X + (a.X+a.Width*(gx+1)/16)*b.Dx()/w
			y0 := b.Min.Y + (a.Y+a.Height*gy/8)*b.Dy()/h
			y1 := b.Min.Y + (a.Y+a.Height*(gy+1)/8)*b.Dy()/h
			n := 0.
			i := (gy*16 + gx) * 3
			for y := y0; y < max(y0+1, y1); y++ {
				for x := x0; x < max(x0+1, x1); x++ {
					var r, g, bl uint32
					if rgba, ok := img.(*image.RGBA); ok && image.Pt(x, y).In(b) {
						i := rgba.PixOffset(x, y)
						r, g, bl = uint32(rgba.Pix[i])*257, uint32(rgba.Pix[i+1])*257, uint32(rgba.Pix[i+2])*257
					} else if nrgba, ok := img.(*image.NRGBA); ok && image.Pt(x, y).In(b) && nrgba.Pix[nrgba.PixOffset(x, y)+3] == 255 {
						i := nrgba.PixOffset(x, y)
						r, g, bl = uint32(nrgba.Pix[i])*257, uint32(nrgba.Pix[i+1])*257, uint32(nrgba.Pix[i+2])*257
					} else {
						r, g, bl, _ = img.At(x, y).RGBA()
					}
					out[i] += float64(r) / 65535
					out[i+1] += float64(g) / 65535
					out[i+2] += float64(bl) / 65535
					n++
				}
			}
			for j := 0; j < 3; j++ {
				out[i+j] /= n
			}
		}
	}
	return out
}

// GreenCoverage measures bright green pixels and does not alone prove eligibility.
func GreenCoverage(img image.Image, a Anchor, w, h int) float64 {
	if img == nil || w <= 0 || h <= 0 || a.X < 0 || a.Y < 0 || a.Width <= 0 || a.Height <= 0 || a.X > w || a.Y > h || a.Width > w-a.X || a.Height > h-a.Y {
		return 0
	}
	b := img.Bounds()
	x0 := b.Min.X + a.X*b.Dx()/w
	x1 := b.Min.X + (a.X+a.Width)*b.Dx()/w
	y0 := b.Min.Y + a.Y*b.Dy()/h
	y1 := b.Min.Y + (a.Y+a.Height)*b.Dy()/h
	if x1 <= x0 || y1 <= y0 {
		return 0
	}
	count := 0
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			r, g, bl, _ := img.At(x, y).RGBA()
			if g > 80*257 && 2*g > r+bl+60*257 {
				count++
			}
		}
	}
	return float64(count) / float64((x1-x0)*(y1-y0))
}

func AnchorError(pixels []float64, anchor Anchor) float64 {
	if anchor.Mode == "desaturated" || anchor.Mode == "saturated" {
		var sum float64
		count := 0
		for i := 0; i < len(pixels); i += 3 {
			high, low := max(pixels[i], pixels[i+1], pixels[i+2]), min(pixels[i], pixels[i+1], pixels[i+2])
			if high > 5000.0/65535 {
				sum += (high - low) / high
				count++
			}
		}
		if count == 0 {
			return 1
		}
		saturation := sum / float64(count)
		if anchor.Mode == "saturated" {
			return 1 - saturation
		}
		return saturation
	}
	var diff float64
	for i, value := range pixels {
		diff += math.Abs(value - anchor.Pixels[i])
	}
	return diff / float64(len(pixels))
}

func (m *PhaseModel) Recognize(img image.Image) (string, []PhaseScore) {
	if img.Bounds().Dx()*m.Height != img.Bounds().Dy()*m.Width {
		return "unknown", nil
	}
	match := "unknown"
	matches := 0
	var scores []PhaseScore
	type region struct{ x, y, width, height int }
	pixels := make(map[region][]float64)
	coverage := make(map[region]float64)
	for _, p := range m.Phases {
		best, matched := math.Inf(1), false
		for _, anchors := range append([][]Anchor{p.Anchors}, p.Variants...) {
			worst, ok := 0.0, true
			for _, a := range anchors {
				key := region{a.X, a.Y, a.Width, a.Height}
				err := 0.0
				if a.Feature == "green-coverage-v1" {
					v, found := coverage[key]
					if !found {
						v = GreenCoverage(img, a, m.Width, m.Height)
						coverage[key] = v
					}
					err = max(a.MinCoverage-v, v-a.MaxCoverage, 0)
					if err > 0 {
						ok = false
					}
				} else {
					v, found := pixels[key]
					if !found {
						v = AnchorPixels(img, a, m.Width, m.Height)
						pixels[key] = v
					}
					err = AnchorError(v, a)
					if err > a.MaxError {
						ok = false
					}
				}
				worst = max(worst, err)
			}
			best = min(best, worst)
			matched = matched || ok
		}
		scores = append(scores, PhaseScore{p.Name, best})
		if matched {
			match = p.Name
			matches++
		}
	}
	if matches != 1 {
		return "unknown", scores
	}
	return match, scores
}
