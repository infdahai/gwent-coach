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
	return DigitCalibration{Method: "area-v1", CandidateOnly: true, ROI: roi, Threshold: 140, Width: 5, Height: 7, MaxDigits: 2, MaxDistance: 9, MinGap: 4, Templates: map[int][]string{0: {"01110110111100111001110011101101110"}, 1: {"00111000110001100011000110001100011", "01111001110011100111001110011100000"}}}
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

// Glyphs are enlarged so that each template cell contains many pixels. This
// exercises pixels that the former center-point sampler never inspected.
func syntheticGlyph(mask string, scale int) *image.Gray {
	im := image.NewGray(image.Rect(0, 0, 5*scale, 7*scale))
	for y := 0; y < 7; y++ {
		for x := 0; x < 5; x++ {
			if mask[y*5+x] == '1' {
				for py := y * scale; py < (y+1)*scale; py++ {
					for px := x * scale; px < (x+1)*scale; px++ {
						im.SetGray(px, py, color.Gray{Y: 255})
					}
				}
			}
		}
	}
	return im
}

func TestDigitsFullCoverageAndUnknowns(t *testing.T) {
	zero := "01110100011000110001100011000101110"
	one := "00100011000010000100001000010011111"
	c := DigitCalibration{Method: "area-v1", CandidateOnly: true, ROI: image.Rect(0, 0, 50, 70), Threshold: 140, Width: 5, Height: 7, MaxDigits: 1, MaxDistance: 2, MinGap: 4, Templates: map[int][]string{0: {zero}, 1: {one}}}
	for _, tc := range []struct {
		mask string
		want int
	}{{zero, 0}, {one, 1}} {
		im := syntheticGlyph(tc.mask, 10)
		if got, ok := c.ReadDigits(im); !ok || got != tc.want {
			t.Fatalf("clean %d: got %d known %t", tc.want, got, ok)
		}
	}
	// Change most pixels in six cells, but preserve every old center sample.
	// The surrounding bounding box and sampled bit mask remain identical.
	im := syntheticGlyph(zero, 10)
	for _, cell := range [][2]int{{1, 1}, {2, 1}, {3, 1}, {1, 2}, {2, 2}, {3, 2}} {
		for y := cell[1] * 10; y < (cell[1]+1)*10; y++ {
			for x := cell[0] * 10; x < (cell[0]+1)*10; x++ {
				if x%10 != 5 || y%10 != 5 {
					im.SetGray(x, y, color.Gray{Y: 255})
				}
			}
		}
	}
	if got, ok := c.ReadDigits(im); ok {
		t.Fatalf("altered unknown accepted as %d", got)
	}
	for _, mask := range []string{
		"11111000010000111111100001000011111", // 2
		"11111000010000111111000010000111111", // 3
		"10001100011000111111000010000100001", // 4
		"11111100001000011111000010000111111", // 5
		"11111100001000011111100011000111111", // 6
		"11111000010001000100010000100001000", // 7
		"11111100011000111111100011000111111", // 8
		"11111100011000111111000010000111111", // 9
	} {
		if got, ok := c.ReadDigits(syntheticGlyph(mask, 10)); ok {
			t.Errorf("unknown %s accepted as %d", mask, got)
		}
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
