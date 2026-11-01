package observe

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gwentcoachmvp/internal/stateestimate"
)

func scoreDigits(roi image.Rectangle) DigitCalibration {
	return DigitCalibration{CandidateOnly: true, ROI: roi, Threshold: 140, Width: 5, Height: 7, MaxDigits: 2, MaxDistance: 2, MinGap: 4, Templates: map[int][]string{0: {"01110110111000110001100011101101110"}, 1: {"00111000110001100011000110001100011", "01111001110011100111001110011100000"}}}
}
func loadRealFrame(t testing.TB, name string) image.Image {
	t.Helper()
	root := os.Getenv("GWENT_REAL_FRAME_DIR")
	if root == "" {
		root = "/tmp/gwent-failure046-20261101"
	}
	f, err := os.Open(filepath.Join(root, name))
	if os.IsNotExist(err) {
		t.Skip("private real-frame corpus unavailable")
	}
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	im, err := jpeg.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	return im
}
func TestRealScoreDigits(t *testing.T) {
	for _, tc := range []struct {
		name       string
		enemy, own int
	}{{"early.jpg", 0, 0}, {"middle.jpg", 11, 0}, {"late.jpg", 11, 0}, {"last.jpg", 11, 0}} {
		t.Run(tc.name, func(t *testing.T) {
			im := loadRealFrame(t, tc.name)
			for _, field := range []struct {
				roi  image.Rectangle
				want int
			}{{image.Rect(1815, 345, 1875, 400), tc.enemy}, {image.Rect(1815, 675, 1875, 730), tc.own}} {
				got, ok := scoreDigits(field.roi).ReadDigits(im)
				if !ok || got != field.want {
					t.Fatalf("got=%d known=%t want=%d", got, ok, field.want)
				}
			}
		})
	}
}
func TestDigitCandidatesCannotEnterGameplayEvidence(t *testing.T) {
	cal, _ := evidenceFixture()
	cal.Digits = map[string]DigitCalibration{"score": scoreDigits(image.Rect(0, 8, 20, 38))}
	if _, err := NewCalibratedEvidenceReader(cal); err == nil {
		t.Fatal("candidate digits accepted as gameplay evidence")
	}
}

func TestDigitsRejectUnknownAndEvidenceAgreement(t *testing.T) {
	c := scoreDigits(image.Rect(0, 0, 20, 30))
	blank := image.NewGray(image.Rect(0, 0, 20, 30))
	if _, ok := c.ReadDigits(blank); ok {
		t.Fatal("blank accepted")
	}
	for y := 5; y < 25; y++ {
		for x := 5; x < 15; x++ {
			blank.Set(x, y, color.White)
		}
	}
	if _, ok := c.ReadDigits(blank); ok {
		t.Fatal("unknown glyph accepted")
	}
	now := time.Now()
	ref := stateestimate.FrameRef{ID: "a", MatchID: "m", CapturedAt: now.Add(-time.Millisecond), Width: 20, Height: 30}
	a := stateestimate.Evidence{Frame: ref, CalibrationID: "c", Scalars: map[string]int{"score": 0}}
	b := a
	b.Frame.ID = "b"
	b.Frame.CapturedAt = now
	b.Scalars = map[string]int{"score": 1}
	result, err := stateestimate.Estimate([2]stateestimate.Evidence{a, b}, now, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := result.Scalars["score"]; ok {
		t.Fatal("disagreeing frames accepted")
	}
}
func BenchmarkJPEGDecodeRealFrame(b *testing.B) {
	data, err := os.ReadFile("/tmp/gwent-failure046-20261101/late.jpg")
	if os.IsNotExist(err) {
		b.Skip("private real-frame corpus unavailable")
	}
	if err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := jpeg.Decode(bytes.NewReader(data)); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRealScoreDigits(b *testing.B) {
	im := loadRealFrame(b, "late.jpg")
	c := scoreDigits(image.Rect(1815, 345, 1875, 400))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, ok := c.ReadDigits(im); !ok {
			b.Fatal("unknown")
		}
	}
}
