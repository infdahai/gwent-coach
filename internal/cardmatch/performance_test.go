package cardmatch

import (
	"image"
	"image/color"
	"testing"
)

type genericImage struct{ image.Image }

func TestNormalizeNativeMatchesGeneric(t *testing.T) {
	for _, img := range []interface {
		image.Image
		Set(int, int, color.Color)
	}{
		image.NewRGBA(image.Rect(7, 9, 137, 159)),
		image.NewNRGBA(image.Rect(7, 9, 137, 159)),
	} {
		for y := 9; y < 159; y++ {
			for x := 7; x < 137; x++ {
				img.Set(x, y, color.RGBA{uint8(x), uint8(y), uint8(x + y), 255})
			}
		}
		roi := ROI{X: 12, Y: 14, Width: 100, Height: 120}
		fast, err := Normalize(img, roi)
		if err != nil {
			t.Fatal(err)
		}
		slow, err := Normalize(genericImage{img}, roi)
		if err != nil || fast != slow {
			t.Fatalf("feature mismatch: %v", err)
		}
		img.Set(20, 20, color.NRGBA{R: 255, A: 100})
		if _, err := Normalize(img, roi); err == nil {
			t.Fatal("accepted transparency")
		}
	}
}

func BenchmarkNormalizeNative(b *testing.B) {
	img := image.NewRGBA(image.Rect(0, 0, 130, 150))
	for y := 0; y < 150; y++ {
		for x := 0; x < 130; x++ {
			img.SetRGBA(x, y, color.RGBA{uint8(x), uint8(y), uint8(x + y), 255})
		}
	}
	roi := ROI{X: 0, Y: 0, Width: 130, Height: 150}
	for _, tc := range []struct {
		name string
		img  image.Image
	}{{"rgba", img}, {"generic", genericImage{img}}} {
		b.Run(tc.name, func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				if _, err := Normalize(tc.img, roi); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
