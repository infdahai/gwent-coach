//go:build windows

package observe

import (
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/kbinani/screenshot"
	"golang.org/x/sys/windows"
)

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")
)

type point struct{ X, Y int32 }
type rect struct{ Left, Top, Right, Bottom int32 }
type gameWindow struct {
	Handle    uintptr `json:"handle"`
	PID       uint32  `json:"pid"`
	Session   uint32  `json:"session"`
	Title     string  `json:"title"`
	Client    rect    `json:"client_screen_rect"`
	DPI       uint32  `json:"dpi"`
	Minimized bool    `json:"minimized"`
}
type report struct {
	Time         string            `json:"time"`
	PID          uint32            `json:"pid"`
	Session      uint32            `json:"session"`
	InputDesktop string            `json:"input_desktop"`
	Foreground   uintptr           `json:"foreground"`
	Displays     []image.Rectangle `json:"displays"`
	Windows      []gameWindow      `json:"game_windows"`
	Screenshot   string            `json:"screenshot,omitempty"`
	Error        string            `json:"error,omitempty"`
}

func sessionID(pid uint32) (uint32, error) {
	var id uint32
	r, _, err := kernel32.NewProc("ProcessIdToSessionId").Call(uintptr(pid), uintptr(unsafe.Pointer(&id)))
	if r == 0 {
		return 0, fmt.Errorf("ProcessIdToSessionId: %w", err)
	}
	return id, nil
}

func desktopName() (string, error) {
	h, _, err := user32.NewProc("OpenInputDesktop").Call(0, 0, 1)
	if h == 0 {
		return "", fmt.Errorf("OpenInputDesktop: %w", err)
	}
	defer user32.NewProc("CloseDesktop").Call(h)
	var buf [256]uint16
	var needed uint32
	r, _, err := user32.NewProc("GetUserObjectInformationW").Call(h, 2, uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Sizeof(buf)), uintptr(unsafe.Pointer(&needed)))
	if r == 0 {
		return "", fmt.Errorf("GetUserObjectInformationW: %w", err)
	}
	return windows.UTF16ToString(buf[:]), nil
}

func findGameWindows() ([]gameWindow, error) {
	var found []gameWindow
	var callbackErr error
	callback := syscall.NewCallback(func(hwnd, _ uintptr) uintptr {
		visible, _, _ := user32.NewProc("IsWindowVisible").Call(hwnd)
		if visible == 0 {
			return 1
		}
		var pid uint32
		user32.NewProc("GetWindowThreadProcessId").Call(hwnd, uintptr(unsafe.Pointer(&pid)))
		process, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
		if err != nil {
			return 1
		}
		var path [32768]uint16
		size := uint32(len(path))
		err = windows.QueryFullProcessImageName(process, 0, &path[0], &size)
		windows.CloseHandle(process)
		if err != nil || !strings.EqualFold(filepath.Base(windows.UTF16ToString(path[:size])), "Gwent.exe") {
			return 1
		}
		session, err := sessionID(pid)
		if err != nil {
			callbackErr = err
			return 0
		}
		var title [512]uint16
		user32.NewProc("GetWindowTextW").Call(hwnd, uintptr(unsafe.Pointer(&title[0])), uintptr(len(title)))
		var client rect
		r, _, err := user32.NewProc("GetClientRect").Call(hwnd, uintptr(unsafe.Pointer(&client)))
		if r == 0 {
			callbackErr = fmt.Errorf("GetClientRect: %w", err)
			return 0
		}
		origin := point{client.Left, client.Top}
		r, _, err = user32.NewProc("ClientToScreen").Call(hwnd, uintptr(unsafe.Pointer(&origin)))
		if r == 0 {
			callbackErr = fmt.Errorf("ClientToScreen: %w", err)
			return 0
		}
		client = rect{origin.X, origin.Y, origin.X + client.Right - client.Left, origin.Y + client.Bottom - client.Top}
		dpi, _, _ := user32.NewProc("GetDpiForWindow").Call(hwnd)
		minimized, _, _ := user32.NewProc("IsIconic").Call(hwnd)
		found = append(found, gameWindow{hwnd, pid, session, windows.UTF16ToString(title[:]), client, uint32(dpi), minimized != 0})
		return 1
	})
	r, _, err := user32.NewProc("EnumWindows").Call(callback, 0)
	if callbackErr != nil {
		return found, callbackErr
	}
	if r == 0 {
		return found, fmt.Errorf("EnumWindows: %w", err)
	}
	return found, nil
}

var dpiOnce sync.Once
var dpiErr error

func enableDPI() error {
	dpiOnce.Do(func() {
		// DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2 is the pseudo-handle -4.
		r, _, err := user32.NewProc("SetProcessDpiAwarenessContext").Call(^uintptr(3))
		if r == 0 {
			dpiErr = fmt.Errorf("SetProcessDpiAwarenessContext: %w", err)
		}
	})
	return dpiErr
}

func Run(out string, logger *log.Logger) (runErr error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	started := time.Now().UTC()
	dir, err := os.MkdirTemp(out, started.Format("20060102T150405Z")+"-")
	if err != nil {
		return err
	}
	rpt := report{Time: started.Format(time.RFC3339Nano), PID: uint32(os.Getpid())}
	defer func() {
		if runErr != nil {
			rpt.Error = runErr.Error()
		}
		data, err := json.MarshalIndent(rpt, "", "  ")
		if err == nil {
			err = os.WriteFile(filepath.Join(dir, "report.json"), append(data, '\n'), 0644)
		}
		runErr = errors.Join(runErr, err)
		logger.Printf("report=%s screenshot=%s error=%s", filepath.Join(dir, "report.json"), rpt.Screenshot, rpt.Error)
	}()

	if err := enableDPI(); err != nil {
		return err
	}
	rpt.Session, err = sessionID(rpt.PID)
	if err != nil {
		return err
	}
	if rpt.Session == 0 {
		return fmt.Errorf("session 0 cannot observe the game desktop; launch using an interactive scheduled task")
	}
	rpt.InputDesktop, err = desktopName()
	if err != nil {
		return err
	}
	if !strings.EqualFold(rpt.InputDesktop, "Default") {
		return fmt.Errorf("input desktop is %q, expected Default", rpt.InputDesktop)
	}
	for i := 0; i < screenshot.NumActiveDisplays(); i++ {
		rpt.Displays = append(rpt.Displays, screenshot.GetDisplayBounds(i))
	}
	rpt.Windows, err = findGameWindows()
	if err != nil {
		return err
	}
	if len(rpt.Windows) == 0 {
		return fmt.Errorf("no visible Gwent.exe window in this desktop")
	}
	rpt.Foreground, _, _ = user32.NewProc("GetForegroundWindow").Call()
	var selected *gameWindow
	for i := range rpt.Windows {
		w := &rpt.Windows[i]
		if w.Handle == rpt.Foreground && w.Session == rpt.Session && !w.Minimized {
			selected = w
			break
		}
	}
	if selected == nil {
		return fmt.Errorf("Gwent must be foreground, restored, and in coach's session; no input was sent")
	}
	bounds := selected.Client
	if bounds.Right <= bounds.Left || bounds.Bottom <= bounds.Top {
		return fmt.Errorf("invalid client rectangle: %+v", bounds)
	}
	img, err := screenshot.Capture(int(bounds.Left), int(bounds.Top), int(bounds.Right-bounds.Left), int(bounds.Bottom-bounds.Top))
	if err != nil {
		return fmt.Errorf("capture: %w", err)
	}
	foreground, _, _ := user32.NewProc("GetForegroundWindow").Call()
	desktop, err := desktopName()
	if err != nil {
		return err
	}
	if foreground != selected.Handle || !strings.EqualFold(desktop, "Default") {
		return fmt.Errorf("foreground or input desktop changed during capture; image discarded")
	}
	name := filepath.Join(dir, "game.png")
	f, err := os.Create(name)
	if err != nil {
		return err
	}
	encodeErr := png.Encode(f, img)
	closeErr := f.Close()
	if err := errors.Join(encodeErr, closeErr); err != nil {
		return err
	}
	rpt.Screenshot = name
	return nil
}
