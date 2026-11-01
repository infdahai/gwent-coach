package cardmatch

import (
	"fmt"
	"image"
	"time"
)

// TemporalLink is a diagnostic correspondence between two explicit image regions.
// It is neither a card identity nor authorization for an action.
type TemporalLink struct {
	Previous int `json:"previous"`
	Current  int `json:"current"`
	Distance int `json:"distance"`
}

// TemporalRegion must be marked visible only when its art is reviewed as unobscured.
type TemporalRegion struct {
	ROI     ROI
	Visible bool
}

// LinkRegions compares same-sized patches with mean absolute RGB difference.
// A link requires unique mutual nearest neighbors, a minimum runner-up margin,
// and caller-supplied limits. Neither localization nor occlusion is inferred.
func LinkRegions(a, b image.Image, beforeBatch, afterBatch string, before, after time.Time, maxGap time.Duration, old, new []TemporalRegion, maxDistance, minGap int) ([]TemporalLink, error) {
	if a == nil || b == nil || beforeBatch == "" || beforeBatch != afterBatch || before.IsZero() || !after.After(before) || maxGap <= 0 || after.Sub(before) > maxGap || len(old) == 0 || len(new) == 0 || len(old) > 10 || len(new) > 10 || maxDistance < 0 || maxDistance > 255 || minGap < 1 || minGap > 255 {
		return nil, fmt.Errorf("invalid temporal evidence or thresholds")
	}
	valid := func(im image.Image, r ROI) bool {
		bounds := im.Bounds()
		return r.Width > 0 && r.Height > 0 && r.X >= bounds.Min.X && r.Y >= bounds.Min.Y && r.X <= bounds.Max.X && r.Y <= bounds.Max.Y && r.Width <= bounds.Max.X-r.X && r.Height <= bounds.Max.Y-r.Y
	}
	for _, r := range old {
		if !valid(a, r.ROI) {
			return nil, fmt.Errorf("invalid previous region")
		}
	}
	for _, r := range new {
		if !valid(b, r.ROI) {
			return nil, fmt.Errorf("invalid current region")
		}
	}
	// At least two alternatives on each side are required to measure ambiguity.
	links := []TemporalLink{}
	countVisible := func(rs []TemporalRegion) int {
		n := 0
		for _, r := range rs {
			if r.Visible {
				n++
			}
		}
		return n
	}
	if countVisible(old) < 2 || countVisible(new) < 2 {
		return links, nil
	}
	distances := make([][]int, len(old))
	for i, oldRegion := range old {
		x := oldRegion.ROI
		distances[i] = make([]int, len(new))
		for j, newRegion := range new {
			y := newRegion.ROI
			if !oldRegion.Visible || !newRegion.Visible {
				distances[i][j] = 256
				continue
			}
			if x.Width != y.Width || x.Height != y.Height {
				return nil, fmt.Errorf("regions must have identical dimensions")
			}
			var sum uint64
			for py := 0; py < x.Height; py++ {
				for px := 0; px < x.Width; px++ {
					ar, ag, ab, aa := a.At(x.X+px, x.Y+py).RGBA()
					br, bg, bb, ba := b.At(y.X+px, y.Y+py).RGBA()
					if aa != 65535 || ba != 65535 {
						return nil, fmt.Errorf("transparent region")
					}
					diff := func(u, v uint32) uint64 {
						if u > v {
							return uint64(u - v)
						}
						return uint64(v - u)
					}
					sum += diff(ar, br) + diff(ag, bg) + diff(ab, bb)
				}
			}
			distances[i][j] = int(sum / (uint64(x.Width) * uint64(x.Height) * 3 * 257))
		}
	}
	for i := range old {
		best, second, jbest := 256, 256, -1
		for j, d := range distances[i] {
			if d < best {
				second, best, jbest = best, d, j
			} else if d < second {
				second = d
			}
		}
		if best > maxDistance || second-best < minGap {
			continue
		}
		reverse, runner, ibest := 256, 256, -1
		for k := range old {
			d := distances[k][jbest]
			if d < reverse {
				runner, reverse, ibest = reverse, d, k
			} else if d < runner {
				runner = d
			}
		}
		if ibest == i && runner-reverse >= minGap {
			links = append(links, TemporalLink{i, jbest, best})
		}
	}
	return links, nil
}
