//go:build windows

package observe

import (
	"encoding/json"
	"errors"
	"fmt"
	"image/png"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unsafe"

	"github.com/kbinani/screenshot"
)

// INPUT has an eight-byte header and a 32-byte union on Windows amd64.
type nativeInput struct {
	Kind uint32
	Pad  uint32
	Data [32]byte
}
type mouseInput struct {
	X, Y              int32
	Data, Flags, Time uint32
	Extra             uintptr
}
type keyInput struct {
	Key, Scan   uint16
	Flags, Time uint32
	Extra       uintptr
}

func stopped(path string) error {
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("paused by stop file %s", path)
	} else if !os.IsNotExist(err) {
		return err
	}
	for _, key := range []uintptr{0x77, 0x11} { // F8 or Ctrl pauses input.
		v, _, _ := user32.NewProc("GetAsyncKeyState").Call(key)
		if v&0x8000 != 0 {
			return fmt.Errorf("paused by F8 or Ctrl")
		}
	}
	return nil
}

func actionWindow(a Action, ref report) (gameWindow, error) {
	if err := stopped(a.StopFile); err != nil {
		return gameWindow{}, err
	}
	desktop, err := desktopName()
	if err != nil {
		return gameWindow{}, err
	}
	if !strings.EqualFold(desktop, "Default") {
		return gameWindow{}, fmt.Errorf("input desktop changed")
	}
	session, err := sessionID(uint32(os.Getpid()))
	if err != nil {
		return gameWindow{}, err
	}
	if session == 0 || session != ref.Session {
		return gameWindow{}, fmt.Errorf("interactive session mismatch")
	}
	foreground, _, _ := user32.NewProc("GetForegroundWindow").Call()
	windows, err := findGameWindows()
	if err != nil {
		return gameWindow{}, err
	}
	for _, current := range windows {
		for _, previous := range ref.Windows {
			if current.Handle == foreground && current.Handle == ref.Foreground && current.Handle == previous.Handle && current.PID == previous.PID && current.Session == session && current.Client == previous.Client && current.DPI == previous.DPI && !current.Minimized {
				return current, nil
			}
		}
	}
	return gameWindow{}, fmt.Errorf("game focus, process, or layout changed; input refused")
}

func Execute(out string, logger *log.Logger, a Action) (runErr error) {
	if err := a.Validate(); err != nil {
		return err
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := enableDPI(); err != nil {
		return err
	}
	data, err := os.ReadFile(a.Reference)
	if err != nil {
		return err
	}
	var ref report
	if err := json.Unmarshal(data, &ref); err != nil {
		return err
	}
	if a.PhaseModel == "" && (ref.ImageSize.X != 0 || ref.ImageSize.Y != 0) {
		for _, window := range ref.Windows {
			if window.Handle == ref.Foreground && (ref.ImageSize.X != int(window.Client.Right-window.Client.Left) || ref.ImageSize.Y != int(window.Client.Bottom-window.Client.Top)) {
				return fmt.Errorf("resized observation is not an input reference; capture native pixels")
			}
		}
	}
	observed, err := time.Parse(time.RFC3339Nano, ref.Time)
	if err != nil {
		return err
	}
	if age := time.Since(observed); age < 0 || age > 2*time.Minute || ref.Error != "" || ref.Screenshot == "" {
		return fmt.Errorf("reference must be a successful observation less than two minutes old")
	}
	w, err := actionWindow(a, ref)
	if err != nil {
		return err
	}
	if (a.Kind == "click" || a.Kind == "hold") && (a.X >= int(w.Client.Right-w.Client.Left) || a.Y >= int(w.Client.Bottom-w.Client.Top)) {
		return fmt.Errorf("click outside game client")
	}
	dir, err := os.MkdirTemp(out, "action-")
	if err != nil {
		return err
	}
	result := struct {
		Action  Action `json:"action"`
		Time    string `json:"time"`
		Outcome string `json:"outcome"`
		Sent    uint32 `json:"events_sent"`
		Error   string `json:"error,omitempty"`
	}{Action: a, Time: time.Now().UTC().Format(time.RFC3339Nano), Outcome: "not_sent"}
	defer func() {
		if runErr != nil {
			result.Error = runErr.Error()
		}
		b, err := json.MarshalIndent(result, "", "  ")
		if err == nil {
			err = os.WriteFile(filepath.Join(dir, "action.json"), append(b, '\n'), 0644)
		}
		runErr = errors.Join(runErr, err)
		logger.Printf("action=%s outcome=%s events=%d error=%s", dir, result.Outcome, result.Sent, result.Error)
	}()
	img, err := screenshot.Capture(int(w.Client.Left), int(w.Client.Top), int(w.Client.Right-w.Client.Left), int(w.Client.Bottom-w.Client.Top))
	if err != nil {
		return err
	}
	f, err := os.Create(filepath.Join(dir, "before.png"))
	if err != nil {
		return err
	}
	encodeErr := png.Encode(f, img)
	if err := errors.Join(encodeErr, f.Close()); err != nil {
		return err
	}
	if _, err := actionWindow(a, ref); err != nil {
		return err
	}
	// Consume the reference before cursor motion, which itself can change UI state.
	claim, err := os.OpenFile(a.Reference+".input-used", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("reference already consumed or cannot be claimed: %w", err)
	}
	if err := claim.Close(); err != nil {
		return err
	}
	var inputs [2]nativeInput
	if a.Kind == "click" || a.Kind == "hold" {
		x, y := w.Client.Left+int32(a.X), w.Client.Top+int32(a.Y)
		result.Outcome = "cursor_move_attempted_requires_review"
		r, _, err := user32.NewProc("SetCursorPos").Call(uintptr(x), uintptr(y))
		if r == 0 {
			return fmt.Errorf("SetCursorPos: %w", err)
		}
		time.Sleep(100 * time.Millisecond)
		if _, err := actionWindow(a, ref); err != nil {
			return err
		}
		var cursor point
		r, _, err = user32.NewProc("GetCursorPos").Call(uintptr(unsafe.Pointer(&cursor)))
		if r == 0 {
			return fmt.Errorf("GetCursorPos: %w", err)
		}
		if cursor.X != x || cursor.Y != y {
			return fmt.Errorf("cursor moved; input refused")
		}
		packed := uintptr(uint64(uint32(x)) | uint64(uint32(y))<<32)
		target, _, _ := user32.NewProc("WindowFromPoint").Call(packed)
		root, _, _ := user32.NewProc("GetAncestor").Call(target, 2)
		if root != w.Handle {
			return fmt.Errorf("click target is covered by another window")
		}
		(*mouseInput)(unsafe.Pointer(&inputs[0].Data[0])).Flags = 0x0002
		(*mouseInput)(unsafe.Pointer(&inputs[1].Data[0])).Flags = 0x0004
	} else {
		for i := range inputs {
			inputs[i].Kind = 1
			(*keyInput)(unsafe.Pointer(&inputs[i].Data[0])).Key = 0x1b
		}
		(*keyInput)(unsafe.Pointer(&inputs[1].Data[0])).Flags = 2
	}
	if _, err := actionWindow(a, ref); err != nil {
		return err
	}
	// Autonomous input rechecks semantic anchors immediately before the event.
	if a.PhaseModel != "" {
		if a.Kind != "click" || a.ExpectedPhase == "" {
			return fmt.Errorf("invalid semantic action")
		}
		if age := time.Since(observed); age < 0 || age > 2*time.Second {
			return fmt.Errorf("autonomous reference expired")
		}
		model := a.phaseModel
		if model == nil {
			return fmt.Errorf("semantic action requires bound in-memory model")
		}
		fresh, err := screenshot.Capture(int(w.Client.Left), int(w.Client.Top), int(w.Client.Right-w.Client.Left), int(w.Client.Bottom-w.Client.Top))
		if err != nil {
			return err
		}
		phase, _ := model.Recognize(fresh)
		if phase != a.ExpectedPhase {
			return fmt.Errorf("semantic input guard rejected phase %q", phase)
		}
		if _, err = actionWindow(a, ref); err != nil {
			return err
		}
	}
	// Cursor motion may have changed the interface, even if no click is delivered.
	if a.Kind != "escape" {
		result.Outcome = "cursor_moved_requires_review"
	}
	if _, err := actionWindow(a, ref); err != nil {
		return err
	}
	if a.Kind != "escape" {
		x, y := w.Client.Left+int32(a.X), w.Client.Top+int32(a.Y)
		var cursor point
		r, _, err := user32.NewProc("GetCursorPos").Call(uintptr(unsafe.Pointer(&cursor)))
		if r == 0 {
			return fmt.Errorf("GetCursorPos: %w", err)
		}
		if cursor.X != x || cursor.Y != y {
			return fmt.Errorf("cursor moved before delivery; input refused")
		}
		packed := uintptr(uint64(uint32(x)) | uint64(uint32(y))<<32)
		target, _, _ := user32.NewProc("WindowFromPoint").Call(packed)
		root, _, _ := user32.NewProc("GetAncestor").Call(target, 2)
		if root != w.Handle {
			return fmt.Errorf("target covered before delivery")
		}
	}
	if a.Kind == "hold" {
		n, _, err := user32.NewProc("SendInput").Call(1, uintptr(unsafe.Pointer(&inputs[0])), unsafe.Sizeof(inputs[0]))
		result.Sent = uint32(n)
		result.Outcome = "unknown_requires_review"
		if n != 1 {
			return fmt.Errorf("mouse down failed: %v", err)
		}
		var holdErr error
		for i := 0; i < 30; i++ {
			time.Sleep(50 * time.Millisecond)
			if _, err := actionWindow(a, ref); err != nil {
				holdErr = err
				break
			}
		}
		n, _, err = user32.NewProc("SendInput").Call(1, uintptr(unsafe.Pointer(&inputs[1])), unsafe.Sizeof(inputs[1]))
		result.Sent += uint32(n)
		if n != 1 {
			return errors.Join(holdErr, fmt.Errorf("mouse release failed: %v", err))
		}
		if holdErr != nil {
			return holdErr
		}
		time.Sleep(900 * time.Millisecond)
		return Run(dir, logger)
	}
	n, _, sendErr := user32.NewProc("SendInput").Call(2, uintptr(unsafe.Pointer(&inputs[0])), unsafe.Sizeof(inputs[0]))
	result.Sent = uint32(n)
	result.Outcome = "unknown_requires_review"
	if n != 2 {
		if n == 1 {
			user32.NewProc("SendInput").Call(1, uintptr(unsafe.Pointer(&inputs[1])), unsafe.Sizeof(inputs[1]))
		}
		return fmt.Errorf("SendInput sent %d/2 events; do not replay: %v", n, sendErr)
	}
	time.Sleep(900 * time.Millisecond)
	if err := Run(filepath.Join(dir), logger); err != nil {
		return err
	}
	logger.Printf("input delivered; inspect after screenshot in %s before another action", dir)
	return nil
}
