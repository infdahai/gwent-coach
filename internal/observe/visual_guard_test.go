package observe

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

func TestCompareReference(t *testing.T) {
	original := image.NewRGBA(image.Rect(0, 0, 2, 2))
	original.Set(0, 0, color.RGBA{R: 255, A: 255})
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, original); err != nil {
		t.Fatal(err)
	}
	changed := image.NewRGBA(original.Bounds())
	copy(changed.Pix, original.Pix)
	changed.Set(1, 1, color.RGBA{B: 1, A: 255})
	for _, tc := range []struct {
		name    string
		data    []byte
		current image.Image
		wantErr bool
	}{
		{"same pixels", encoded.Bytes(), original, false},
		{"changed pixel", encoded.Bytes(), changed, true},
		{"changed dimensions", encoded.Bytes(), image.NewRGBA(image.Rect(0, 0, 1, 2)), true},
		{"invalid reference", []byte("not png"), original, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := compareReference(bytes.NewReader(tc.data), tc.current)
			if (err != nil) != tc.wantErr {
				t.Fatalf("compareReference error = %v, want error %v", err, tc.wantErr)
			}
		})
	}
}
