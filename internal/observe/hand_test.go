package observe

import (
	"image"
	"testing"
)

func TestHandHover(t *testing.T) {
	// Manually selected visible portion of the sixth card in the archived
	// 1920x1080 training-current.png; this is geometry, not identification.
	a, err := HandHover(image.Pt(1920, 1080), image.Rect(925, 955, 995, 1075), "frame/report.json", "pause")
	if err != nil || a.Kind != "hover" || a.X != 960 || a.Y != 1015 {
		t.Fatalf("action=%+v err=%v", a, err)
	}
	for _, r := range []image.Rectangle{image.Rect(-1, 0, 10, 10), image.Rect(1900, 1000, 1921, 1080), image.Rect(0, 0, 0, 1)} {
		if _, err := HandHover(image.Pt(1920, 1080), r, "frame/report.json", "pause"); err == nil {
			t.Fatalf("accepted %v", r)
		}
	}
	if _, err := HandHover(image.Pt(1920, 1080), image.Rect(1, 1, 10, 10), "", "pause"); err == nil {
		t.Fatal("accepted missing reference")
	}
}
