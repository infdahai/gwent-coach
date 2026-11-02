package controlgwent

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	"gwentcoachmvp/internal/observe"
)

func TestSessionMustUseHostStopLatch(t *testing.T) {
	dir := t.TempDir()
	stop := filepath.Join(dir, "STOP")
	loopPath := filepath.Join(dir, "loop.json")
	sessionPath := filepath.Join(dir, "session.json")
	write := func(path string, v any) {
		t.Helper()
		data, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(sessionPath, observe.NativeSessionConfig{LoopConfig: "loop.json"})
	b := Backend{SessionConfig: sessionPath, StopFile: stop}
	write(loopPath, observe.NativeLoopConfig{StopFile: filepath.Join(dir, "other-stop")})
	if b.Validate() == nil {
		t.Fatal("accepted ineffective stop latch")
	}
	write(loopPath, observe.NativeLoopConfig{StopFile: stop})
	if err := b.Validate(); err != nil {
		t.Fatal(err)
	}
	write(loopPath, observe.NativeLoopConfig{StopFile: "STOP"})
	if b.Validate() == nil {
		t.Fatal("accepted relative session stop")
	}
}

func TestDesktopOnlyHostCannotRunSession(t *testing.T) {
	b := Backend{}
	if err := b.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := b.RunSession(context.Background(), t.TempDir(), io.Discard); err == nil {
		t.Fatal("unconfigured session accepted")
	}
}
