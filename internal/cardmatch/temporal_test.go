package cardmatch

import (
	"image"
	"image/color"
	"testing"
	"time"
)

func TestLinkRegions(t *testing.T) {
	im := image.NewRGBA(image.Rect(0, 0, 30, 10))
	for y := 0; y < 10; y++ {
		for x := 0; x < 30; x++ {
			v := uint8((x/10)*90 + 20)
			im.Set(x, y, color.RGBA{v, v, v, 255})
		}
	}
	regions := []TemporalRegion{{ROI: ROI{0, 0, 10, 10}, Visible: true}, {ROI: ROI{10, 0, 10, 10}, Visible: true}, {ROI: ROI{20, 0, 10, 10}, Visible: true}}
	now := time.Now()
	perm := []TemporalRegion{regions[2], regions[0], regions[1]}
	links, err := LinkRegions(im, im, "batch", "batch", now, now.Add(time.Second), 2*time.Second, regions, perm, 2, 10)
	if err != nil || len(links) != 3 || links[0].Current != 1 {
		t.Fatal(links, err)
	}
	duplicate := []TemporalRegion{regions[0], regions[0], regions[2]}
	links, err = LinkRegions(im, im, "batch", "batch", now, now.Add(time.Second), 2*time.Second, duplicate, duplicate, 2, 10)
	if err != nil || len(links) != 1 || links[0].Previous != 2 {
		t.Fatal(links, err)
	}
	perm[0].Visible = false
	perm[1].Visible = false
	links, err = LinkRegions(im, im, "batch", "batch", now, now.Add(time.Second), 2*time.Second, regions, perm, 2, 10)
	if err != nil || len(links) != 0 {
		t.Fatal(links, err)
	}
	if _, err = LinkRegions(im, im, "batch", "batch", now, now.Add(3*time.Second), 2*time.Second, regions, regions, 2, 10); err == nil {
		t.Fatal("accepted gap")
	}
	if _, err = LinkRegions(im, im, "batch", "other", now, now.Add(time.Second), 2*time.Second, regions, regions, 2, 10); err == nil {
		t.Fatal("accepted different batches")
	}
	if _, err = LinkRegions(im, im, "batch", "batch", now, now, 2*time.Second, regions, regions, 2, 10); err == nil {
		t.Fatal("accepted same timestamp")
	}
}
