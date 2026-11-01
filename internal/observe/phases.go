package observe

import (
	"encoding/json"
	"fmt"
	"gwentcoachmvp/internal/uiphase"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"os"
	"path/filepath"
)

// Compatibility names retain existing callers; recognition is owned by uiphase.
type PhaseModel = uiphase.PhaseModel
type Phase = uiphase.Phase
type Anchor = uiphase.Anchor
type PhaseScore = uiphase.PhaseScore

func AnchorPixels(img image.Image, a Anchor, w, h int) []float64 {
	return uiphase.AnchorPixels(img, a, w, h)
}
func GreenCoverage(img image.Image, a Anchor, w, h int) float64 {
	return uiphase.GreenCoverage(img, a, w, h)
}
func anchorError(pixels []float64, a Anchor) float64 { return uiphase.AnchorError(pixels, a) }

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

type phaseAnchorDiagnostic struct {
	Phase                              string `json:"phase"`
	Variant                            int    `json:"variant"`
	Mode                               string `json:"mode,omitempty"`
	Feature                            string `json:"feature,omitempty"`
	X, Y, Width, Height                int
	Error, MaxError                    float64
	Coverage, MinCoverage, MaxCoverage float64
}

func phaseAnchorDiagnostics(m *PhaseModel, img image.Image) []phaseAnchorDiagnostic {
	var out []phaseAnchorDiagnostic
	for _, phase := range m.Phases {
		for variant, anchors := range append([][]Anchor{phase.Anchors}, phase.Variants...) {
			for _, a := range anchors {
				d := phaseAnchorDiagnostic{Phase: phase.Name, Variant: variant, Mode: a.Mode, Feature: a.Feature, X: a.X, Y: a.Y, Width: a.Width, Height: a.Height, MaxError: a.MaxError, MinCoverage: a.MinCoverage, MaxCoverage: a.MaxCoverage}
				if a.Feature == "green-coverage-v1" {
					d.Coverage = GreenCoverage(img, a, m.Width, m.Height)
					d.Error = max(a.MinCoverage-d.Coverage, d.Coverage-a.MaxCoverage, 0)
				} else {
					d.Error = anchorError(AnchorPixels(img, a, m.Width, m.Height), a)
				}
				out = append(out, d)
			}
		}
	}
	return out
}

func DiagnosePhaseFile(modelPath, imagePath string, w io.Writer) error {
	model, err := LoadPhases(modelPath)
	if err != nil {
		return err
	}
	img, err := readImage(imagePath)
	if err != nil {
		return err
	}
	phase, scores := model.Recognize(img)
	return json.NewEncoder(w).Encode(struct {
		Phase   string
		Scores  []PhaseScore
		Anchors any
	}{phase, scores, phaseAnchorDiagnostics(model, img)})
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
	m, err := buildPhaseModel(labelsPath)
	if err != nil {
		return err
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(out, b, 0600)
}

func buildPhaseModel(labelsPath string) (*PhaseModel, error) {
	b, e := os.ReadFile(labelsPath)
	if e != nil {
		return nil, e
	}
	var labels PhaseLabels
	if e = json.Unmarshal(b, &labels); e != nil {
		return nil, e
	}
	m := PhaseModel{Width: labels.Width, Height: labels.Height}
	if m.Width < 1 || m.Height < 1 {
		return nil, fmt.Errorf("invalid label dimensions")
	}
	for _, s := range labels.Samples {
		img, e := readImage(filepath.Join(filepath.Dir(labelsPath), s.Image))
		if e != nil {
			return nil, e
		}
		if img.Bounds().Dx()*m.Height != img.Bounds().Dy()*m.Width {
			return nil, fmt.Errorf("label image aspect ratio mismatch: %s", s.Image)
		}
		p := Phase{Name: s.Name, Anchors: s.Anchors}
		for i := range p.Anchors {
			a := &p.Anchors[i]
			if a.X < 0 || a.Y < 0 || a.Width < 16 || a.Height < 8 || a.X+a.Width > m.Width || a.Y+a.Height > m.Height {
				return nil, fmt.Errorf("invalid label ROI")
			}
			if a.Feature == "" {
				a.Pixels = AnchorPixels(img, *a, m.Width, m.Height)
			}
		}
		found := false
		for i := range m.Phases {
			if m.Phases[i].Name == p.Name {
				m.Phases[i].Variants = append(m.Phases[i].Variants, p.Anchors)
				found = true
				break
			}
		}
		if !found {
			m.Phases = append(m.Phases, p)
		}
	}
	if e = m.Validate(); e != nil {
		return nil, e
	}
	return &m, nil
}
