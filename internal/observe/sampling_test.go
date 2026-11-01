package observe

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"
	"time"
)

type failingWriter struct{ err error }

func (w failingWriter) Write([]byte) (int, error) { return 0, w.err }

func TestTimedWriter(t *testing.T) {
	var b bytes.Buffer
	w := &timedWriter{writer: &b}
	if _, err := encodeFrame(w, image.NewRGBA(image.Rect(0, 0, 8, 8)), Sampling{JPEGQuality: 85}); err != nil {
		t.Fatal(err)
	}
	if _, err := jpeg.Decode(&b); err != nil || w.duration <= 0 {
		t.Fatalf("invalid JPEG or timing: %v, %v", err, w.duration)
	}
	want := errors.New("write failed")
	w = &timedWriter{writer: failingWriter{want}}
	if _, err := encodeFrame(w, image.NewRGBA(image.Rect(0, 0, 8, 8)), Sampling{JPEGQuality: 85}); !errors.Is(err, want) {
		t.Fatalf("write error = %v, want %v", err, want)
	}
}

func TestSamplingValidation(t *testing.T) {
	for _, s := range []Sampling{{}, {Count: 10001}, {Count: 2, Interval: time.Second}, {Count: 2, Interval: time.Millisecond, StopFile: "stop"}, {Count: 1, MaxWidth: -1}, {Count: 1, JPEGQuality: 101}} {
		if s.Validate() == nil {
			t.Fatalf("accepted invalid settings: %+v", s)
		}
	}
	if err := (Sampling{Count: 2, Interval: time.Second, StopFile: "stop", JPEGQuality: 60, MaxWidth: 960}).Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestEncodeFrame(t *testing.T) {
	src := image.NewRGBA(image.Rect(10, 20, 110, 70))
	for y := 20; y < 70; y++ {
		for x := 10; x < 110; x++ {
			src.Set(x, y, color.RGBA{255, 0, 0, 255})
		}
	}
	for _, quality := range []int{0, 60} {
		var b bytes.Buffer
		size, err := encodeFrame(&b, src, Sampling{MaxWidth: 50, JPEGQuality: quality})
		if err != nil {
			t.Fatal(err)
		}
		if size != image.Pt(50, 25) {
			t.Fatal(size)
		}
		var decoded image.Image
		if quality == 0 {
			decoded, err = png.Decode(&b)
		} else {
			decoded, err = jpeg.Decode(&b)
		}
		if err != nil {
			t.Fatal(err)
		}
		r, g, blue, _ := decoded.At(0, 0).RGBA()
		if r < 60000 || g > 1000 || blue > 1000 {
			t.Fatal("lost source offset/color")
		}
	}
	var b bytes.Buffer
	size, err := encodeFrame(&b, src, Sampling{MaxWidth: 200})
	if err != nil || size != image.Pt(100, 50) {
		t.Fatalf("unexpected upscale: %v %v", size, err)
	}
}
