package observe

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"gwentcoachmvp/internal/stateestimate"
)

func TestBuildEvidence(t *testing.T) {
	dir := t.TempDir()
	fixture, imgs := evidenceFixture()
	writeJSON := func(name string, v any) string {
		t.Helper()
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, b, 0600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	stage := writeJSON("stage.json", fixture.Stage)
	b, err := os.ReadFile(stage)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(b)
	f, err := os.Create(filepath.Join(dir, "frame.png"))
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, imgs[0]); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	labels := PhaseLabels{Width: 192, Height: 64}
	labels.Samples = append(labels.Samples, struct {
		Name, Image string
		Anchors     []Anchor
	}{Name: "one", Image: "frame.png", Anchors: []Anchor{{X: 32, Y: 0, Width: 16, Height: 8, MaxError: .01}, {X: 48, Y: 0, Width: 16, Height: 8, MaxError: .01}}})
	writeJSON("labels.json", labels)
	spec := EvidenceBuildManifest{StageModel: "stage.json", StageSHA256: hex.EncodeToString(hash[:]), Round: &EvidenceBuildGroup{Labels: "labels.json", Values: map[string]json.RawMessage{"one": json.RawMessage(`{"Round":1,"Wins":[0,0]}`)}, RequiredRGB: []RGBRequirement{{ROI: image.Rect(32, 0, 48, 8), MaxError: .01}}}}
	manifest := writeJSON("build.json", spec)
	output := filepath.Join(dir, "output.json")
	if err := BuildEvidence(manifest, output); err != nil {
		t.Fatal(err)
	}
	reader, err := loadEvidenceReader(output, fileSHA(t, output))
	if err != nil {
		t.Fatal(err)
	}
	got := reader.readFacts(stateestimate.FrameRef{}, imgs[0], "turn")
	if got.Facts.Round == nil || got.Facts.Round.Round != 1 || got.Facts.Turn != nil || got.Facts.Cards != nil {
		t.Fatalf("unexpected facts: %+v", got.Facts)
	}
	if err := BuildEvidence(manifest, output); err == nil {
		t.Fatal("overwrote existing output")
	}
	original, _ := os.ReadFile(output)
	cases := []struct {
		name   string
		change func(*EvidenceBuildManifest)
	}{
		{"wrong hash", func(s *EvidenceBuildManifest) { s.StageSHA256 = string(make([]byte, 64)) }},
		{"missing field", func(s *EvidenceBuildManifest) { s.Round.Values["one"] = json.RawMessage(`{"Round":1}`) }},
		{"null field", func(s *EvidenceBuildManifest) { s.Round.Values["one"] = json.RawMessage(`{"Round":1,"Wins":null}`) }},
		{"short array", func(s *EvidenceBuildManifest) { s.Round.Values["one"] = json.RawMessage(`{"Round":1,"Wins":[0]}`) }},
		{"duplicate field", func(s *EvidenceBuildManifest) {
			s.Round.Values["one"] = json.RawMessage(`{"Round":1,"Wins":[0,0],"Round":0}`)
		}},
		{"case alias", func(s *EvidenceBuildManifest) {
			s.Round.Values["one"] = json.RawMessage(`{"Round":1,"Wins":[0,0],"round":0}`)
		}},
		{"cards case alias", func(s *EvidenceBuildManifest) {
			s.Cards = &EvidenceBuildGroup{Labels: s.Round.Labels, RequiredRGB: s.Round.RequiredRGB, Values: map[string]json.RawMessage{"one": json.RawMessage(`{"Visible":[],"HandOrder":[],"visible":null}`)}}
		}},
		{"nested duplicate", func(s *EvidenceBuildManifest) {
			s.Targets = []EvidenceBuildGroup{{Labels: s.Round.Labels, RequiredRGB: s.Round.RequiredRGB, Values: map[string]json.RawMessage{"one": json.RawMessage(`{"Kind":"control","ID":"pass","LayoutID":"layout","Bounds":{"Min":{"X":1,"Y":1,"X":2},"Max":{"X":5,"Y":5}}}`)}}}
		}},
		{"nested alias", func(s *EvidenceBuildManifest) {
			s.Targets = []EvidenceBuildGroup{{Labels: s.Round.Labels, RequiredRGB: s.Round.RequiredRGB, Values: map[string]json.RawMessage{"one": json.RawMessage(`{"Kind":"control","ID":"pass","LayoutID":"layout","Bounds":{"Min":{"X":1,"Y":1,"x":2},"Max":{"X":5,"Y":5}}}`)}}}
		}},
		{"missing rgb", func(s *EvidenceBuildManifest) { s.Round.RequiredRGB = nil }},
		{"wrong label", func(s *EvidenceBuildManifest) {
			s.Round.Values = map[string]json.RawMessage{"other": json.RawMessage(`{"Round":1,"Wins":[0,0]}`)}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var copy EvidenceBuildManifest
			bb, _ := json.Marshal(spec)
			if err := json.Unmarshal(bb, &copy); err != nil {
				t.Fatal(err)
			}
			tc.change(&copy)
			writeJSON("bad.json", copy)
			bad := filepath.Join(dir, "bad-output.json")
			if err := BuildEvidence(filepath.Join(dir, "bad.json"), bad); err == nil {
				t.Fatal("accepted invalid manifest")
			}
			if _, err := os.Stat(bad); !os.IsNotExist(err) {
				t.Fatal("created output on validation failure")
			}
		})
	}
	after, _ := os.ReadFile(output)
	if string(original) != string(after) {
		t.Fatal("existing output changed")
	}
}

// This optional private check reads existing files only. It does not synthesize
// capture identities or timestamps and cannot establish estimator eligibility.
func TestPrivateEvidenceCalibration(t *testing.T) {
	path := os.Getenv("GWENT_EVIDENCE_PRIVATE_CONFIG")
	if path == "" {
		t.Skip("private calibration not supplied")
	}
	r, err := loadEvidenceReader(path, fileSHA(t, path))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.cal.Stage.Phases) != 12 || len(r.cal.Targets) != 1 {
		t.Fatal("expected 12 stages and one target")
	}
	img, err := readImage(os.Getenv("GWENT_EVIDENCE_PRIVATE_IMAGE"))
	if err != nil {
		t.Fatal(err)
	}
	stage, _ := r.cal.Stage.Recognize(img)
	if target := readFact(r.cal.Targets[0], img); target == nil || target.ID != "pass" {
		t.Fatalf("expected pass target: %+v", target)
	}
	if r.cal.Round != nil || r.cal.Turn != nil || r.cal.Totals != nil || r.cal.Cards != nil || r.cal.Outcome != nil {
		t.Fatal("unexpected game facts")
	}
	t.Logf("stage=%s; pass target recognized; all game fact groups unknown; partial profile cannot yield complete view", stage)
}

func fileSHA(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}
