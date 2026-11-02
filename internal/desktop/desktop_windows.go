//go:build windows

package desktop

import (
	"bytes"
	"context"
	"fmt"
	"image/png"
	"os"
	"runtime"
	"time"
	"unsafe"

	"github.com/kbinani/screenshot"
	"golang.org/x/sys/windows"
)

var user32 = windows.NewLazySystemDLL("user32.dll")
var kernel32 = windows.NewLazySystemDLL("kernel32.dll")

type point struct{ X, Y int32 }

// Use per-thread DPI awareness: observe has its own process-wide initialization.
func dpiThread() (func(), error) {
	runtime.LockOSThread()
	old, _, err := user32.NewProc("SetThreadDpiAwarenessContext").Call(^uintptr(3))
	if old == 0 {
		runtime.UnlockOSThread()
		return nil, fmt.Errorf("DPI awareness: %w", err)
	}
	return func() { user32.NewProc("SetThreadDpiAwarenessContext").Call(old); runtime.UnlockOSThread() }, nil
}

func processSession(pid uint32) (uint32, error) {
	var session uint32
	r, _, err := kernel32.NewProc("ProcessIdToSessionId").Call(uintptr(pid), uintptr(unsafe.Pointer(&session)))
	if r == 0 {
		return 0, fmt.Errorf("process session: %w", err)
	}
	return session, nil
}

func objectName(h uintptr) (string, error) {
	var buf [256]uint16
	var needed uint32
	r, _, err := user32.NewProc("GetUserObjectInformationW").Call(h, 2, uintptr(unsafe.Pointer(&buf[0])), unsafe.Sizeof(buf), uintptr(unsafe.Pointer(&needed)))
	if r == 0 {
		return "", fmt.Errorf("desktop name: %w", err)
	}
	return windows.UTF16ToString(buf[:]), nil
}

func foreground() (Window, error) {
	var w Window
	h, _, err := user32.NewProc("OpenInputDesktop").Call(0, 0, 1)
	if h == 0 {
		return w, fmt.Errorf("input desktop: %w", err)
	}
	defer user32.NewProc("CloseDesktop").Call(h)
	w.Desktop, err = objectName(h)
	if err != nil {
		return w, err
	}
	tid, _, _ := kernel32.NewProc("GetCurrentThreadId").Call()
	threadDesktop, _, _ := user32.NewProc("GetThreadDesktop").Call(tid)
	name, err := objectName(threadDesktop)
	if err != nil {
		return w, err
	}
	if w.Desktop != "Default" || name != w.Desktop {
		return w, fmt.Errorf("interactive Default desktop required")
	}
	w.Handle, _, _ = user32.NewProc("GetForegroundWindow").Call()
	if w.Handle == 0 {
		return w, fmt.Errorf("no foreground window")
	}
	r, _, err := user32.NewProc("GetWindowThreadProcessId").Call(w.Handle, uintptr(unsafe.Pointer(&w.PID)))
	if r == 0 {
		return w, fmt.Errorf("foreground process: %w", err)
	}
	w.Session, err = processSession(w.PID)
	if err != nil {
		return w, err
	}
	ownSession, err := processSession(uint32(os.Getpid()))
	if err != nil {
		return w, err
	}
	if ownSession == 0 || w.Session != ownSession {
		return w, fmt.Errorf("foreground must belong to host interactive session")
	}
	visible, _, _ := user32.NewProc("IsWindowVisible").Call(w.Handle)
	minimized, _, _ := user32.NewProc("IsIconic").Call(w.Handle)
	w.Minimized = minimized != 0
	if visible == 0 || w.Minimized {
		return w, fmt.Errorf("foreground must be visible and restored")
	}
	r, _, err = user32.NewProc("GetClientRect").Call(w.Handle, uintptr(unsafe.Pointer(&w.Client)))
	if r == 0 {
		return w, fmt.Errorf("client rectangle: %w", err)
	}
	origin := point{w.Client.Left, w.Client.Top}
	r, _, err = user32.NewProc("ClientToScreen").Call(w.Handle, uintptr(unsafe.Pointer(&origin)))
	if r == 0 {
		return w, fmt.Errorf("client origin: %w", err)
	}
	w.Client = Rect{origin.X, origin.Y, origin.X + w.Client.Right - w.Client.Left, origin.Y + w.Client.Bottom - w.Client.Top}
	dpi, _, _ := user32.NewProc("GetDpiForWindow").Call(w.Handle)
	w.DPI = uint32(dpi)
	if w.DPI == 0 || w.Client.Right <= w.Client.Left || w.Client.Bottom <= w.Client.Top {
		return w, fmt.Errorf("invalid foreground DPI or geometry")
	}
	return w, nil
}

func Take(ctx context.Context) (Capture, error) {
	var c Capture
	done, err := dpiThread()
	if err != nil {
		return c, err
	}
	defer done()
	if err = ctx.Err(); err != nil {
		return c, err
	}
	c.Window, err = foreground()
	if err != nil {
		return c, err
	}
	b := c.Window.Client
	c.At = time.Now().UTC()
	img, err := screenshot.Capture(int(b.Left), int(b.Top), int(b.Right-b.Left), int(b.Bottom-b.Top))
	if err != nil {
		return c, err
	}
	current, err := foreground()
	if err != nil {
		return c, err
	}
	if current != c.Window {
		return c, fmt.Errorf("foreground changed during capture")
	}
	var buf bytes.Buffer
	if err = png.Encode(&buf, img); err != nil {
		return c, err
	}
	c.PNG = buf.Bytes()
	return c, ctx.Err()
}

func guard(ctx context.Context, ref Capture, x, y int, stopFile string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := os.Stat(stopFile); err == nil {
		return fmt.Errorf("paused by stop file")
	} else if !os.IsNotExist(err) {
		return err
	}
	for _, key := range []uintptr{0x77, 0x11} {
		v, _, _ := user32.NewProc("GetAsyncKeyState").Call(key)
		if v&0x8000 != 0 {
			return fmt.Errorf("paused by F8 or Ctrl")
		}
	}
	if age := time.Since(ref.At); age < 0 || age > MaxAge {
		return fmt.Errorf("capture expired")
	}
	current, err := foreground()
	if err != nil {
		return err
	}
	if current != ref.Window {
		return fmt.Errorf("foreground HWND/PID/session/desktop/DPI/geometry changed")
	}
	if x < 0 || y < 0 || int64(x) >= int64(current.Client.Right)-int64(current.Client.Left) || int64(y) >= int64(current.Client.Bottom)-int64(current.Client.Top) {
		return fmt.Errorf("coordinates outside client")
	}
	return nil
}

type nativeInput struct {
	Kind, Pad uint32
	Data      [32]byte
}
type mouseInput struct {
	X, Y              int32
	Data, Flags, Time uint32
	Extra             uintptr
}

// Click assumes the caller has durably claimed the reference before calling.
// Even cursor motion consumes a reference; partial delivery must never be replayed.
func Click(ctx context.Context, ref Capture, x, y int, stopFile string) (Receipt, error) {
	return pointerInput(ctx, ref, x, y, stopFile, false)
}

// Hold shares Click's reference and pointer guards, with a fixed 1.5s press.
func Hold(ctx context.Context, ref Capture, x, y int, stopFile string) (Receipt, error) {
	return pointerInput(ctx, ref, x, y, stopFile, true)
}

func pointerInput(ctx context.Context, ref Capture, x, y int, stopFile string, hold bool) (Receipt, error) {
	receipt := Receipt{Outcome: "not_sent"}
	done, err := dpiThread()
	if err != nil {
		return receipt, err
	}
	defer done()
	if unsafe.Sizeof(uintptr(0)) != 8 {
		return receipt, fmt.Errorf("desktop input requires 64-bit Windows")
	}
	if err = guard(ctx, ref, x, y, stopFile); err != nil {
		return receipt, err
	}
	px, py := ref.Window.Client.Left+int32(x), ref.Window.Client.Top+int32(y)
	receipt.Outcome = "cursor_move_attempted"
	r, _, err := user32.NewProc("SetCursorPos").Call(uintptr(px), uintptr(py))
	if r == 0 {
		return receipt, fmt.Errorf("SetCursorPos: %w", err)
	}
	receipt.Outcome = "cursor_moved"
	select {
	case <-ctx.Done():
		return receipt, ctx.Err()
	case <-time.After(100 * time.Millisecond):
	}
	checkPointer := func() error {
		if err := guard(ctx, ref, x, y, stopFile); err != nil {
			return err
		}
		var cursor point
		r, _, err := user32.NewProc("GetCursorPos").Call(uintptr(unsafe.Pointer(&cursor)))
		if r == 0 {
			return fmt.Errorf("GetCursorPos: %w", err)
		}
		if cursor.X != px || cursor.Y != py {
			return fmt.Errorf("cursor moved")
		}
		target, _, _ := user32.NewProc("WindowFromPoint").Call(uintptr(uint64(uint32(px)) | uint64(uint32(py))<<32))
		root, _, _ := user32.NewProc("GetAncestor").Call(target, 2)
		if root != ref.Window.Handle {
			return fmt.Errorf("target covered by another window")
		}
		return nil
	}
	if err = checkPointer(); err != nil {
		return receipt, err
	}
	var inputs [2]nativeInput
	(*mouseInput)(unsafe.Pointer(&inputs[0].Data[0])).Flags = 0x0002
	(*mouseInput)(unsafe.Pointer(&inputs[1].Data[0])).Flags = 0x0004
	if err = guard(ctx, ref, x, y, stopFile); err != nil {
		return receipt, err
	}
	if hold {
		return deliverHold(ctx, checkPointer, func(down bool) (uint32, error) {
			i := 1
			if down {
				i = 0
			}
			n, _, sendErr := user32.NewProc("SendInput").Call(1, uintptr(unsafe.Pointer(&inputs[i])), unsafe.Sizeof(inputs[i]))
			if n != 1 {
				return uint32(n), fmt.Errorf("SendInput sent %d/1 events; do not replay: %v", n, sendErr)
			}
			return uint32(n), nil
		})
	}
	n, _, sendErr := user32.NewProc("SendInput").Call(2, uintptr(unsafe.Pointer(&inputs[0])), unsafe.Sizeof(inputs[0]))
	receipt.EventsSent = uint32(n)
	receipt.Outcome = "partial_or_unknown"
	if n != 2 {
		if n == 1 { // Release only; never replay the click.
			released, _, _ := user32.NewProc("SendInput").Call(1, uintptr(unsafe.Pointer(&inputs[1])), unsafe.Sizeof(inputs[1]))
			receipt.EventsSent += uint32(released)
		}
		return receipt, fmt.Errorf("SendInput sent %d/2 events; do not replay: %v", n, sendErr)
	}
	receipt.Outcome = "delivered_unconfirmed"
	return receipt, nil
}
