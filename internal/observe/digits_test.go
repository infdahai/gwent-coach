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
	return DigitCalibration{ROI: roi, Threshold: 140, Width: 5, Height: 7, MaxDigits: 2, MaxDistance: 2, MinGap: 4, Templates: map[int][]string{0: {"01110110111000110001100011101101110"}, 1: {"00111000110001100011000110001100011", "01111001110011100111001110011100000"}}}
}
func loadRealFrame(t testing.TB, name string) image.Image {
	t.Helper()
	f, err := os.Open(filepath.Join("/tmp/gwent-failure046-20261101", name))
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
func TestDigitReaderEvidenceChain(t *testing.T) {
	cal, frames := evidenceFixture()
	cal.Digits = map[string]DigitCalibration{"score": scoreDigits(image.Rect(0, 8, 20, 38))}
	// Draw a scaled zero using the calibrated binary mask.
	mask := cal.Digits["score"].Templates[0][0]
	for y := 0; y < 7; y++ {
		for x := 0; x < 5; x++ {
			if mask[y*5+x] == '1' {
				for dy := 0; dy < 3; dy++ {
					for dx := 0; dx < 3; dx++ {
						frames[0].Set(x*3+dx+2, y*3+dy+10, color.White)
					}
				}
			}
		}
	}
	reader, err := NewCalibratedEvidenceReader(cal)
	if err != nil {
		t.Fatal(err)
	}
	pair, now := readEvidencePair(t, reader, frames[0])
	estimated, err := stateestimate.Estimate(pair, now, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := estimated.Scalars["score"]; !ok || v != 0 {
		t.Fatalf("score=%d known=%t", v, ok)
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
