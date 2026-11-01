package observe

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"strings"
	"testing"
	"time"

	"gwentcoachmvp/internal/cards"
)

func TestHoverAnnotationOCRAndUnknown(t *testing.T) {
	catalog, err := cards.Load("../../data/cards/14.10.0/cn/cards.json")
	if err != nil {
		t.Fatal(err)
	}
	var audit auditDeck
	audit.DataVersion = "14.10.0"
	audit.Cards = append(audit.Cards, struct {
		CardID string `json:"card_id"`
		Name   string `json:"name_cn"`
		Count  int    `json:"count"`
	}{"202245", "异兽园看管", 1})
	names, err := deckNames(catalog, audit)
	if err != nil {
		t.Fatal(err)
	}
	cfg := HoverAnnotationConfig{ExpectedPhase: "turn", NeutralPoint: image.Pt(900, 500), Slots: []HoverSlot{{Point: image.Pt(450, 950), NameROI: image.Rect(600, 750, 900, 820)}, {Point: image.Pt(550, 950), NameROI: image.Rect(600, 750, 900, 820)}}}
	frames, hovers, reads := 0, 0, 0
	var out bytes.Buffer
	capture := func() (navFrame, error) {
		frames++
		return navFrame{Reference: fmt.Sprint(frames), Captured: time.Now(), Phase: "turn", Width: 1920, Height: 1080}, nil
	}
	err = runHoverAnnotation(cfg, names, capture, func(navFrame, image.Point) error { hovers++; return nil }, func(navFrame, image.Rectangle) (string, error) {
		reads++
		switch reads {
		case 1, 4:
			return "", nil
		case 2:
			return "异兽园 看管", nil
		case 3, 5:
			return "异兽园看管", nil
		default:
			return "无法识别", nil
		}
	}, &out)
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(&out)
	var first, second hoverResult
	if err := dec.Decode(&first); err != nil {
		t.Fatal(err)
	}
	if err := dec.Decode(&second); err != nil {
		t.Fatal(err)
	}
	if first.Status != "candidate" || first.CardID != 202245 || second.Status != "unknown" || second.CardID != 0 || hovers != 4 {
		t.Fatalf("first=%+v second=%+v hovers=%d", first, second, hovers)
	}
}
func TestMatchDeckTextRejectsAmbiguousNames(t *testing.T) {
	names := map[string]int{"异兽园看管": 202245, "狼群": 132403, "莫拉汉姆家仆从": 202550}
	if id, _ := matchDeckText("莫 拉 汉 姆 家 亻 卜 从", names); id != 202550 {
		t.Fatalf("split Chinese character: %d", id)
	}
	if id, name := matchDeckText("更多信息 异 兽 园 看 管 部署", names); id != 202245 || name != "异兽园看管" {
		t.Fatalf("id=%d name=%q", id, name)
	}
	if id, _ := matchDeckText("异兽园看管 狼群", names); id != 0 {
		t.Fatal("accepted ambiguous OCR")
	}
}

func TestHoverAnnotationRejectsVisibleNeutralTooltip(t *testing.T) {
	cfg := HoverAnnotationConfig{ExpectedPhase: "turn", NeutralPoint: image.Pt(900, 500), Slots: []HoverSlot{{Point: image.Pt(450, 950)}}}
	frames, hovers := 0, 0
	err := runHoverAnnotation(cfg, map[string]int{"异兽园看管": 202245, "狼群": 132403}, func() (navFrame, error) {
		frames++
		return navFrame{Reference: fmt.Sprint(frames), Captured: time.Now(), Phase: "turn", Width: 1920, Height: 1080}, nil
	}, func(navFrame, image.Point) error { hovers++; return nil }, func(navFrame, image.Rectangle) (string, error) { return "异兽园看管 狼群", nil }, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "tooltip remains visible") || hovers != 1 {
		t.Fatalf("err=%v hovers=%d", err, hovers)
	}
}

func TestHoverAnnotationStopsOnUncertainInput(t *testing.T) {
	cfg := HoverAnnotationConfig{ExpectedPhase: "turn", NeutralPoint: image.Pt(900, 500), Slots: []HoverSlot{{Point: image.Pt(1, 1), NameROI: image.Rect(1, 1, 20, 20)}}}
	sends := 0
	err := runHoverAnnotation(cfg, map[string]int{"x": 1}, func() (navFrame, error) {
		return navFrame{Reference: "one", Captured: time.Now(), Phase: "turn", Width: 1920, Height: 1080}, nil
	}, func(navFrame, image.Point) error { sends++; return errors.New("cursor error") }, func(navFrame, image.Rectangle) (string, error) { t.Fatal("unexpected OCR"); return "", nil }, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "hover unresolved") || sends != 1 {
		t.Fatalf("err=%v sends=%d", err, sends)
	}
}
