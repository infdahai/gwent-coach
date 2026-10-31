package observe

import (
	"encoding/json"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"math"
	"os"
	"path/filepath"
)

// PhaseModel uses operator-labelled, local-only UI crops. No account images are embedded.
type PhaseModel struct {
	Width, Height int
	Phases        []Phase
}
type Phase struct {
	Name    string
	Anchors []Anchor
}
type Anchor struct {
	X, Y, Width, Height int
	Pixels              []float64
	MaxError            float64
}
type PhaseScore struct {
	Name  string
	Error float64
}

func LoadPhases(path string) (*PhaseModel, error) {
	b, e := os.ReadFile(path)
	if e != nil {
		return nil, e
	}
	return parsePhases(b)
}

func parsePhases(b []byte) (*PhaseModel, error) {
	var m PhaseModel
	if e := json.Unmarshal(b, &m); e != nil {
		return nil, e
	}
	return &m, m.Validate()
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
		for _, a := range p.Anchors {
			if a.X < 0 || a.Y < 0 || a.Width < 1 || a.Height < 1 || a.X+a.Width > m.Width || a.Y+a.Height > m.Height || len(a.Pixels) != 16*8*3 || a.MaxError <= 0 || a.MaxError > 0.15 || math.IsNaN(a.MaxError) {
				return fmt.Errorf("invalid anchor in %s", p.Name)
			}
			for _, v := range a.Pixels {
				if v < 0 || v > 1 || math.IsNaN(v) || math.IsInf(v, 0) {
					return fmt.Errorf("invalid pixel")
				}
			}
		}
	}
	return nil
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
					r, g, bl, _ := img.At(x, y).RGBA()
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
func (m *PhaseModel) Recognize(img image.Image) (string, []PhaseScore) {
	if img.Bounds().Dx()*m.Height != img.Bounds().Dy()*m.Width {
		return "unknown", nil
	}
	match := "unknown"
	matches := 0
	var scores []PhaseScore
	for _, p := range m.Phases {
		worst := 0.
		ok := true
		for _, a := range p.Anchors {
			v := AnchorPixels(img, a, m.Width, m.Height)
			err := 0.
			for i, x := range v {
				err += math.Abs(x - a.Pixels[i])
			}
			err /= float64(len(v))
			worst = max(worst, err)
			if err > a.MaxError {
				ok = false
			}
		}
		scores = append(scores, PhaseScore{p.Name, worst})
		if ok {
			match = p.Name
			matches++
		}
	}
	if matches != 1 {
		return "unknown", scores
	}
	return match, scores
}
func ClassifyFile(modelPath, imagePath string, w io.Writer) error {
	m, e := LoadPhases(modelPath)
	if e != nil {
		return e
	}
	img, e := readImage(imagePath)
	if e != nil {
		return e
	}
	phase, scores := m.Recognize(img)
	return json.NewEncoder(w).Encode(struct {
		Phase  string
		Scores []PhaseScore
	}{phase, scores})
}

func readImage(path string) (image.Image, error) {
	f, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	img, _, e := image.Decode(f)
	return img, e
}

// LabelModel creates anchors from explicit human-labelled sample regions.
type PhaseLabels struct {
	Width, Height int
	Samples       []struct {
		Name, Image string
		Anchors     []Anchor
	}
}

func BuildPhases(labelsPath, out string) error {
	b, e := os.ReadFile(labelsPath)
	if e != nil {
		return e
	}
	var labels PhaseLabels
	if e = json.Unmarshal(b, &labels); e != nil {
		return e
	}
	m := PhaseModel{Width: labels.Width, Height: labels.Height}
	if m.Width < 1 || m.Height < 1 {
		return fmt.Errorf("invalid label dimensions")
	}
	for _, s := range labels.Samples {
		img, e := readImage(filepath.Join(filepath.Dir(labelsPath), s.Image))
		if e != nil {
			return e
		}
		if img.Bounds().Dx()*m.Height != img.Bounds().Dy()*m.Width {
			return fmt.Errorf("label image aspect ratio mismatch: %s", s.Image)
		}
		p := Phase{Name: s.Name, Anchors: s.Anchors}
		for i := range p.Anchors {
			a := &p.Anchors[i]
			if a.X < 0 || a.Y < 0 || a.Width < 16 || a.Height < 8 || a.X+a.Width > m.Width || a.Y+a.Height > m.Height {
				return fmt.Errorf("invalid label ROI")
			}
			a.Pixels = AnchorPixels(img, *a, m.Width, m.Height)
		}
		m.Phases = append(m.Phases, p)
	}
	if e = m.Validate(); e != nil {
		return e
	}
	b, e = json.MarshalIndent(m, "", "  ")
	if e != nil {
		return e
	}
	return os.WriteFile(out, b, 0600)
}
