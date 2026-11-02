package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gwentcoachmvp/internal/control"
	"gwentcoachmvp/internal/desktop"
)

type cliBackend struct{}

func (cliBackend) Capture(context.Context) (desktop.Capture, error) {
	return desktop.Capture{At: time.Now(), PNG: []byte("png-fixture"), Window: desktop.Window{Client: desktop.Rect{Right: 100, Bottom: 100}}}, nil
}
func (cliBackend) Click(context.Context, desktop.Capture, int, int, string) (desktop.Receipt, error) {
	return desktop.Receipt{Outcome: "partial_or_unknown", EventsSent: 1}, errors.New("input interrupted")
}
func (cliBackend) RunSession(context.Context, string, io.Writer) error {
	return errors.New("session unavailable")
}

func TestControlCLICaptureAndFailedClick(t *testing.T) {
	dir := t.TempDir()
	token := strings.Repeat("a", 64)
	tokenPath := filepath.Join(dir, "token")
	if err := os.WriteFile(tokenPath, []byte(token+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	h, err := control.NewHost(control.Config{Token: token, Root: filepath.Join(dir, "state"), StopFile: filepath.Join(dir, "STOP")}, cliBackend{})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	s := httptest.NewServer(h)
	defer s.Close()
	var output, diagnostics bytes.Buffer
	id := control.NewID()
	image := filepath.Join(dir, "frame.png")
	err = runControl([]string{"capture", "-url", s.URL, "-token-file", tokenPath, "-id", id, "-image", image}, &output, &diagnostics)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(image)
	if err != nil || string(data) != "png-fixture" {
		t.Fatalf("image=%q err=%v", data, err)
	}
	if !strings.Contains(output.String(), id) || !strings.Contains(diagnostics.String(), "request_id="+id) {
		t.Fatal("missing receipt or pre-request ID")
	}
	output.Reset()
	diagnostics.Reset()
	err = runControl([]string{"click", "-url", s.URL, "-token-file", tokenPath, "-reference", id, "-x", "10", "-y", "20"}, &output, &diagnostics)
	if err == nil || !strings.Contains(err.Error(), "input interrupted") || !strings.Contains(output.String(), "partial_or_unknown") {
		t.Fatalf("hidden partial receipt output=%s err=%v", &output, err)
	}
	if strings.Contains(output.String()+diagnostics.String(), token) {
		t.Fatal("token leaked")
	}
}
