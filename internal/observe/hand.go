package observe

import (
	"fmt"
	"image"
)

// HandHover maps an explicitly located visible hand rectangle to one reviewed
// hover action. Bounds are in native client pixels; no identity or turn is
// inferred. A caller must supply a fresh native-size observation reference.
func HandHover(client image.Point, card image.Rectangle, reference, stopFile string) (Action, error) {
	bounds := image.Rectangle{Max: client}
	if client.X <= 0 || client.Y <= 0 || card.Empty() || !card.In(bounds) {
		return Action{}, fmt.Errorf("card rectangle must be inside the client")
	}
	p := image.Pt(card.Min.X+card.Dx()/2, card.Min.Y+card.Dy()/2)
	a := Action{Kind: "hover", X: p.X, Y: p.Y, Reference: reference, StopFile: stopFile}
	if err := a.Validate(); err != nil {
		return Action{}, err
	}
	return a, nil
}
