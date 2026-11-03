package observe

import (
	"errors"
	"fmt"
	"image"
	"image/png"
	"os"
)

// guardNamedCardFrame verifies identity on a fresh select-time image: it writes
// the frame to one transient PNG in the caller's private run directory,
// recognizes the hovered tooltip text, and requires that text to name cardID.
// It never guesses a card position and never falls back to another identity.
// Missing inputs, encode/OCR/cleanup failures and a different recognized
// identity all reject the input.
func guardNamedCardFrame(img image.Image, dir string, recognize func(string) (string, error), names []ObservedCardName, cardID int) (err error) {
	if img == nil || dir == "" || recognize == nil || len(names) == 0 {
		return fmt.Errorf("named card guard requires a fresh image, private directory, OCR and configured names")
	}
	file, err := os.CreateTemp(dir, "named-card-guard-*.png")
	if err != nil {
		return fmt.Errorf("named card guard: %w", err)
	}
	path := file.Name()
	defer func() {
		if removeErr := os.Remove(path); removeErr != nil {
			err = errors.Join(err, fmt.Errorf("named card guard cleanup: %w", removeErr))
		}
	}()
	if encodeErr := png.Encode(file, img); encodeErr != nil {
		return errors.Join(fmt.Errorf("named card guard encode: %w", encodeErr), file.Close())
	}
	if err = file.Close(); err != nil {
		return fmt.Errorf("named card guard close: %w", err)
	}
	text, err := recognize(path)
	if err != nil {
		return fmt.Errorf("named card guard OCR: %w", err)
	}
	if id := namedCard(names, text); id != cardID {
		return fmt.Errorf("named card guard rejected identity %d", id)
	}
	return nil
}
