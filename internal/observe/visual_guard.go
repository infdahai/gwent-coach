package observe

import (
	"fmt"
	"image"
	"image/png"
	"io"
)

// compareReference rejects any visual change since the reviewed observation.
func compareReference(reference io.Reader, current image.Image) error {
	previous, err := png.Decode(reference)
	if err != nil {
		return fmt.Errorf("decode reference screenshot: %w", err)
	}
	if previous.Bounds().Size() != current.Bounds().Size() || previous.Bounds().Empty() {
		return fmt.Errorf("reference screenshot dimensions changed")
	}
	p, c := previous.Bounds(), current.Bounds()
	for y := 0; y < p.Dy(); y++ {
		for x := 0; x < p.Dx(); x++ {
			pr, pg, pb, pa := previous.At(p.Min.X+x, p.Min.Y+y).RGBA()
			cr, cg, cb, ca := current.At(c.Min.X+x, c.Min.Y+y).RGBA()
			if pr != cr || pg != cg || pb != cb || pa != ca {
				return fmt.Errorf("screen changed since reference; input refused")
			}
		}
	}
	return nil
}
