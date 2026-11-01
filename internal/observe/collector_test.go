package observe

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"strings"
	"testing"
	"time"
)

func TestReadOnlyCollectionRejectsOldCalibration(t *testing.T) {
	c := scoreDigits(image.Rect(0, 0, 20, 30))
	c.Method = ""
	captured := false
	err := CollectReadOnly(context.Background(), CollectionConfig{Count: 1, Interval: 100 * time.Millisecond, Digits: map[string]DigitCalibration{"score": c}}, func(context.Context) (CollectionFrame, error) {
		captured = true
		return CollectionFrame{}, nil
	}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "recalibrated") || captured {
		t.Fatalf("old calibration: captured=%t err=%v", captured, err)
	}
	if _, ok := c.ReadDigits(image.NewGray(c.ROI)); ok {
		t.Fatal("old calibration accepted")
	}
}

func TestReadOnlyCollectionBoundedAndMarkedUntrusted(t *testing.T) {
	var out bytes.Buffer
	n := 0
	err := CollectReadOnly(context.Background(), CollectionConfig{Count: 2, Interval: 100 * time.Millisecond, Digits: map[string]DigitCalibration{"score": scoreDigits(image.Rect(0, 0, 20, 30))}}, func(context.Context) (CollectionFrame, error) {
		n++
		return CollectionFrame{Reference: string(rune('a' + n)), Captured: time.Now(), Image: image.NewGray(image.Rect(0, 0, 20, 30))}, nil
	}, &out)
	if err != nil || n != 2 {
		t.Fatalf("captures=%d err=%v", n, err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 {
		t.Fatal(out.String())
	}
	for _, line := range lines {
		var r CollectionRecord
		if err = json.Unmarshal([]byte(line), &r); err != nil || r.Trust != "unverified_candidate_not_for_actions" || len(r.Unknown) != 1 {
			t.Fatalf("record=%+v err=%v", r, err)
		}
	}
}
