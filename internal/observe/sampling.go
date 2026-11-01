package observe

import (
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"log"
	"os"
	"time"
)

// Sampling controls bounded, observation-only collection on the interactive desktop.
type Sampling struct {
	Count       int
	Interval    time.Duration
	MaxWidth    int
	JPEGQuality int
	StopFile    string
}

func (s Sampling) Validate() error {
	if s.Count < 1 || s.Count > 10000 {
		return fmt.Errorf("sample count must be between 1 and 10000")
	}
	if s.Interval < 0 || (s.Count > 1 && s.Interval < 100*time.Millisecond) {
		return fmt.Errorf("repeated samples require at least 100ms interval")
	}
	if s.MaxWidth < 0 || s.JPEGQuality < 0 || s.JPEGQuality > 100 {
		return fmt.Errorf("invalid image width or JPEG quality")
	}
	if s.Count > 1 && s.StopFile == "" {
		return fmt.Errorf("repeated sampling requires a stop file")
	}
	return nil
}

func Sample(out string, logger *log.Logger, s Sampling) error {
	if err := s.Validate(); err != nil {
		return err
	}
	for i := 0; i < s.Count; i++ {
		if s.StopFile != "" {
			if _, err := os.Stat(s.StopFile); err == nil {
				return fmt.Errorf("sampling paused by stop file")
			} else if !os.IsNotExist(err) {
				return err
			}
		}
		started := time.Now()
		if err := runSample(out, logger, s); err != nil {
			return err
		}
		if i+1 < s.Count {
			time.Sleep(max(0, s.Interval-time.Since(started)))
		}
	}
	return nil
}

func encodeFrame(w io.Writer, src image.Image, s Sampling) (image.Point, error) {
	bounds := src.Bounds()
	size := bounds.Size()
	if s.MaxWidth > 0 && size.X > s.MaxWidth {
		size = image.Pt(s.MaxWidth, max(1, size.Y*s.MaxWidth/size.X))
		dst := image.NewRGBA(image.Rectangle{Max: size})
		for y := 0; y < size.Y; y++ {
			for x := 0; x < size.X; x++ {
				dst.Set(x, y, src.At(bounds.Min.X+x*bounds.Dx()/size.X, bounds.Min.Y+y*bounds.Dy()/size.Y))
			}
		}
		src = dst
	}
	if s.JPEGQuality > 0 {
		return size, jpeg.Encode(w, src, &jpeg.Options{Quality: s.JPEGQuality})
	}
	encoder := png.Encoder{CompressionLevel: png.BestSpeed}
	return size, encoder.Encode(w, src)
}
