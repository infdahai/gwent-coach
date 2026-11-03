package observe

import (
	"errors"
	"image"
	"image/color"
	"os"
	"strings"
	"testing"
)

func identityGuardImage() image.Image {
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for x := 0; x < 4; x++ {
		for y := 0; y < 4; y++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 60), G: uint8(y * 60), B: 100, A: 255})
		}
	}
	return img
}

var identityGuardNames = []ObservedCardName{{Name: "艾斯卡尔", CardID: 112202}, {Name: "维瑟米尔", CardID: 112203}}

func TestGuardNamedCardFrameMatchesIdentity(t *testing.T) {
	dir := t.TempDir()
	called := false
	err := guardNamedCardFrame(identityGuardImage(), dir, func(path string) (string, error) {
		called = true
		b, readErr := os.ReadFile(path)
		if readErr != nil || len(b) == 0 {
			t.Fatalf("guard frame unreadable: %v", readErr)
		}
		return "卡 牌 大 师 维 瑟 米 尔 猎 魔 人", nil
	}, identityGuardNames, 112203)
	if err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("OCR was not called")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("guard left files behind: %v", entries)
	}
}

func TestGuardNamedCardFrameRejectsOtherIdentity(t *testing.T) {
	err := guardNamedCardFrame(identityGuardImage(), t.TempDir(), func(string) (string, error) {
		return "卡 牌 大 师 艾 斯 卡 尔", nil
	}, identityGuardNames, 112203)
	if err == nil || !strings.Contains(err.Error(), "rejected identity") {
		t.Fatalf("expected identity rejection, got %v", err)
	}
}

func TestGuardNamedCardFramePropagatesOCRError(t *testing.T) {
	dir := t.TempDir()
	err := guardNamedCardFrame(identityGuardImage(), dir, func(string) (string, error) {
		return "", errors.New("ocr unavailable")
	}, identityGuardNames, 112203)
	if err == nil || !strings.Contains(err.Error(), "OCR") {
		t.Fatalf("expected OCR error, got %v", err)
	}
	entries, readErr := os.ReadDir(dir)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("guard left files after OCR failure: %v", entries)
	}
}

func TestGuardNamedCardFrameRequiresInputs(t *testing.T) {
	recognize := func(string) (string, error) { return "", nil }
	if err := guardNamedCardFrame(nil, t.TempDir(), recognize, identityGuardNames, 112203); err == nil {
		t.Fatal("nil image accepted")
	}
	if err := guardNamedCardFrame(identityGuardImage(), "", recognize, identityGuardNames, 112203); err == nil {
		t.Fatal("empty directory accepted")
	}
	if err := guardNamedCardFrame(identityGuardImage(), t.TempDir(), nil, identityGuardNames, 112203); err == nil {
		t.Fatal("nil OCR accepted")
	}
	if err := guardNamedCardFrame(identityGuardImage(), t.TempDir(), recognize, nil, 112203); err == nil {
		t.Fatal("empty names accepted")
	}
}
