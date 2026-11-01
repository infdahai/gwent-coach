//go:build windows

package observe

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestReportOptionalTimings(t *testing.T) {
	b, err := json.Marshal(report{})
	if err != nil || strings.Contains(string(b), `"timings"`) {
		t.Fatalf("default report contains timings: %s, %v", b, err)
	}
	b, err = json.Marshal(report{Timings: &captureTimings{CaptureMS: milliseconds(0), EncodeMS: milliseconds(0), WriteMS: milliseconds(0)}})
	if err != nil || !strings.Contains(string(b), `"timings":{"capture_ms":0,"encode_ms":0,"write_ms":0}`) {
		t.Fatalf("unexpected timing report: %s, %v", b, err)
	}
}
