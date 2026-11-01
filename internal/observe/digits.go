package observe

import (
	"fmt"
	"image"
)

// DigitCalibration binds a numeric ROI to foreground brightness and glyph
// templates. Templates are row-major binary masks of equal dimensions.
type DigitCalibration struct {
	ROI           image.Rectangle
	Threshold     uint8
	Width, Height int
	MaxDigits     int
	MaxDistance   int
	MinGap        int
	Templates     map[int][]string
	// CandidateOnly must be explicitly set: template matches are not sufficient
	// evidence for gameplay or for detecting partial occlusion.
	CandidateOnly bool
}

func (c DigitCalibration) validate(bounds image.Rectangle) error {
	if c.ROI.Empty() || !c.ROI.In(bounds) || c.Width < 1 || c.Height < 1 || c.Width*c.Height > 64 || c.MaxDigits < 1 || c.MaxDigits > 9 || c.MaxDistance < 0 || c.MaxDistance >= c.Width*c.Height || c.MinGap < 1 || len(c.Templates) < 2 {
		return fmt.Errorf("invalid digit calibration")
	}
	for digit, samples := range c.Templates {
		if digit < 0 || digit > 9 || len(samples) == 0 {
			return fmt.Errorf("invalid digit templates")
		}
		for _, sample := range samples {
			if len(sample) != c.Width*c.Height {
				return fmt.Errorf("invalid digit template size")
			}
			for _, b := range sample {
				if b != '0' && b != '1' {
					return fmt.Errorf("invalid digit template mask")
				}
			}
		}
	}
	return nil
}

// ReadDigits returns false for missing, ambiguous, or out-of-calibration pixels.
// MaxDistance is the sum of per-cell foreground mismatch fractions; MinGap
// is the minimum difference between the best and second-best digit distances.
func (c DigitCalibration) ReadDigits(img image.Image) (int, bool) {
	if img == nil || c.validate(img.Bounds()) != nil {
		return 0, false
	}
	foreground := func(x, y int) bool {
		r, g, b, _ := img.At(x, y).RGBA()
		return (299*r+587*g+114*b)/1000 >= uint32(c.Threshold)*257
	}
	var ranges [][2]int
	for x := c.ROI.Min.X; x < c.ROI.Max.X; {
		occupied := func(col int) bool {
			for y := c.ROI.Min.Y; y < c.ROI.Max.Y; y++ {
				if foreground(col, y) {
					return true
				}
			}
			return false
		}
		if !occupied(x) {
			x++
			continue
		}
		start := x
		for x < c.ROI.Max.X && occupied(x) {
			x++
		}
		ranges = append(ranges, [2]int{start, x})
	}
	if len(ranges) == 0 || len(ranges) > c.MaxDigits {
		return 0, false
	}
	value := 0
	for _, span := range ranges {
		ymin, ymax := c.ROI.Max.Y, c.ROI.Min.Y
		for y := c.ROI.Min.Y; y < c.ROI.Max.Y; y++ {
			for x := span[0]; x < span[1]; x++ {
				if foreground(x, y) {
					if y < ymin {
						ymin = y
					}
					if y+1 > ymax {
						ymax = y + 1
					}
				}
			}
		}
		if span[1]-span[0] < c.Width || ymax-ymin < c.Height {
			return 0, false
		}
		// Compare every pixel, giving each normalized cell equal weight even
		// when the glyph dimensions are not multiples of the template size.
		coverage := make([]float64, c.Width*c.Height)
		for y := 0; y < c.Height; y++ {
			y0 := ymin + y*(ymax-ymin)/c.Height
			y1 := ymin + (y+1)*(ymax-ymin)/c.Height
			for x := 0; x < c.Width; x++ {
				x0 := span[0] + x*(span[1]-span[0])/c.Width
				x1 := span[0] + (x+1)*(span[1]-span[0])/c.Width
				count := 0
				for py := y0; py < y1; py++ {
					for px := x0; px < x1; px++ {
						if foreground(px, py) {
							count++
						}
					}
				}
				coverage[y*c.Width+x] = float64(count) / float64((x1-x0)*(y1-y0))
			}
		}
		best, second, winner := float64(c.Width*c.Height+1), float64(c.Width*c.Height+1), -1
		for digit, samples := range c.Templates {
			distance := float64(c.Width*c.Height + 1)
			for _, s := range samples {
				d := 0.0
				for i, b := range s {
					if b == '1' {
						d += 1 - coverage[i]
					} else {
						d += coverage[i]
					}
				}
				if d < distance {
					distance = d
				}
			}
			if distance < best {
				second, best, winner = best, distance, digit
			} else if distance < second {
				second = distance
			}
		}
		if best > float64(c.MaxDistance) || second-best < float64(c.MinGap) {
			return 0, false
		}
		value = value*10 + winner
	}
	return value, true
}
