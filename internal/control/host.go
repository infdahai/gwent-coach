package control

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gwentcoachmvp/internal/desktop"
)

type Config struct {
	Token    string
	Root     string
	StopFile string
}

type Host struct {
	cfg      Config
	backend  Backend
	mu       sync.Mutex
	state    string
	active   string
	cancel   context.CancelFunc
	done     chan struct{}
	closed   bool
	shutdown chan struct{}
	last     *Receipt
	fault    string
	refID    string
	ref      desktop.Capture
}

func NewHost(cfg Config, backend Backend) (*Host, error) {
	if len(cfg.Token) < 32 || strings.ContainsAny(cfg.Token, " \r\n\t") {
		return nil, fmt.Errorf("token must contain at least 32 non-whitespace characters")
	}
	if cfg.Root == "" || cfg.StopFile == "" || backend == nil {
		return nil, fmt.Errorf("state directory, stop file and backend required")
	}
	var err error
	cfg.Root, err = filepath.Abs(cfg.Root)
	if err != nil {
		return nil, err
	}
	cfg.StopFile, err = filepath.Abs(cfg.StopFile)
	if err != nil {
		return nil, err
	}
	for _, dir := range []string{cfg.Root, filepath.Join(cfg.Root, "requests"), filepath.Join(cfg.Root, "captures"), filepath.Join(cfg.Root, "sessions"), filepath.Dir(cfg.StopFile)} {
		if err = os.MkdirAll(dir, 0700); err != nil {
			return nil, err
		}
	}
	// This file is intentionally not cleared on an unclean exit. An operator must
	// reconcile receipts and game journals before removing an abandoned host lock.
	if err = writeExclusive(filepath.Join(cfg.Root, "host.lock"), []byte(fmt.Sprint(os.Getpid()))); err != nil {
		return nil, fmt.Errorf("host state already owned or needs recovery: %w", err)
	}
	return &Host{cfg: cfg, backend: backend, state: "idle", shutdown: make(chan struct{})}, nil
}

func writeExclusive(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, werr := f.Write(data)
	return errors.Join(werr, f.Sync(), f.Close())
}

func (h *Host) requestPath(id, suffix string) string {
	return filepath.Join(h.cfg.Root, "requests", id+suffix)
}

func (h *Host) persistResult(r Receipt) error {
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	return writeExclusive(h.requestPath(r.ID, ".result.json"), data)
}

func (h *Host) receipt(id string) (Receipt, error) {
	var r Receipt
	data, err := os.ReadFile(h.requestPath(id, ".result.json"))
	if os.IsNotExist(err) {
		data, err = os.ReadFile(h.requestPath(id, ".claim.json"))
	}
	if err != nil {
		return r, err
	}
	err = json.Unmarshal(data, &r)
	return r, err
}

func (h *Host) Status() Status {
	h.mu.Lock()
	defer h.mu.Unlock()
	s := Status{State: h.state, ActiveID: h.active, Last: h.last, Error: h.fault}
	_, err := os.Stat(h.cfg.StopFile)
	s.StopSignaled = err == nil
	if err != nil && !os.IsNotExist(err) {
		s.Error = errors.Join(errors.New(s.Error), err).Error()
	}
	return s
}

// Stop persists a latch and requests cancellation. State stays stopping until
// the backend actually returns. Stop never removes game journals or claims.
func (h *Host) Stop() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.stopLocked()
}

func (h *Host) stopLocked() error {
	h.refID = ""
	h.ref = desktop.Capture{}
	err := writeExclusive(h.cfg.StopFile, []byte("resident control stop\n"))
	if os.IsExist(err) {
		err = nil
	}
	if h.cancel != nil {
		h.state = "stopping"
		h.cancel()
	}
	if err != nil {
		h.fault = err.Error()
	}
	return err
}

// ShutdownRequested lets the listener owner join work and close the server.
func (h *Host) ShutdownRequested() <-chan struct{} { return h.shutdown }

// Close joins the active operation before releasing host ownership.
func (h *Host) Close() error {
	h.mu.Lock()
	h.closed = true
	var err error
	if h.cancel != nil {
		err = h.stopLocked()
	}
	done := h.done
	h.mu.Unlock()
	if done != nil {
		<-done
	}
	return errors.Join(err, os.Remove(filepath.Join(h.cfg.Root, "host.lock")))
}

func respond(w http.ResponseWriter, code int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(value)
}

func (h *Host) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// No browser origin, fetch metadata, CORS, cookies or token query support.
	if r.Header.Get("Origin") != "" || r.Header.Get("Sec-Fetch-Site") != "" || ValidateAddress(r.Host) != nil {
		respond(w, http.StatusForbidden, map[string]string{"error": "browser or non-loopback host refused"})
		return
	}
	if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+h.cfg.Token)) != 1 {
		respond(w, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
		return
	}
	switch {
	case r.Method == "GET" && r.URL.Path == "/v1/status":
		respond(w, http.StatusOK, h.Status())
	case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/v1/receipts/"):
		id := strings.TrimPrefix(r.URL.Path, "/v1/receipts/")
		if !validID(id) {
			respond(w, 400, map[string]string{"error": "invalid request ID"})
			return
		}
		h.mu.Lock()
		receipt, err := h.receipt(id)
		h.mu.Unlock()
		if err != nil {
			respond(w, 404, map[string]string{"error": "receipt unavailable"})
			return
		}
		respond(w, 200, receipt)
	case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/v1/captures/"):
		id := strings.TrimPrefix(r.URL.Path, "/v1/captures/")
		if !validID(id) {
			respond(w, 400, map[string]string{"error": "invalid capture ID"})
			return
		}
		data, err := os.ReadFile(filepath.Join(h.cfg.Root, "captures", id+".json"))
		if err != nil {
			respond(w, 404, map[string]string{"error": "capture unavailable"})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(data)
	case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/v1/logs/"):
		id := strings.TrimPrefix(r.URL.Path, "/v1/logs/")
		if !validID(id) {
			respond(w, 400, map[string]string{"error": "invalid request ID"})
			return
		}
		data, err := os.ReadFile(filepath.Join(h.cfg.Root, "sessions", id, "output.log"))
		if err != nil {
			respond(w, 404, map[string]string{"error": "log unavailable"})
			return
		}
		respond(w, 200, map[string]string{"log": string(data)})
	case r.Method == "POST" && r.URL.Path == "/v1/commands":
		if r.Header.Get("Content-Type") != "application/json" {
			respond(w, 415, map[string]string{"error": "application/json required"})
			return
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
		decoder.DisallowUnknownFields()
		var cmd Command
		err := decoder.Decode(&cmd)
		if err == nil {
			var extra any
			if decoder.Decode(&extra) != io.EOF {
				err = fmt.Errorf("one command required")
			}
		}
		if err != nil || !validID(cmd.ID) {
			respond(w, 400, map[string]string{"error": "invalid command or request ID"})
			return
		}
		h.command(w, cmd)
	default:
		respond(w, 404, map[string]string{"error": "unknown endpoint"})
	}
}

func (h *Host) command(w http.ResponseWriter, cmd Command) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if prior, err := h.receipt(cmd.ID); err == nil {
		// Return the original receipt even when payload differs; never execute again.
		respond(w, http.StatusConflict, prior)
		return
	} else if !os.IsNotExist(err) {
		respond(w, 500, map[string]string{"error": "cannot inspect request claim"})
		return
	}
	r := Receipt{ID: cmd.ID, Command: cmd.Kind, State: "accepted", At: time.Now().UTC()}
	if cmd.Kind == "click" || cmd.Kind == "hold" {
		r.Intent = &InputIntent{Reference: cmd.Reference, X: cmd.X, Y: cmd.Y}
	}
	data, _ := json.Marshal(r)
	if err := writeExclusive(h.requestPath(cmd.ID, ".claim.json"), data); err != nil {
		respond(w, 500, map[string]string{"error": "cannot durably claim request"})
		return
	}
	reject := func(err error) {
		r.State = "rejected"
		r.Error = err.Error()
		if err := h.persistResult(r); err != nil {
			h.fault = err.Error()
			r.Error += "; receipt persistence failed"
		}
		h.last = &r
		respond(w, 409, r)
	}
	if h.closed {
		reject(fmt.Errorf("host closing"))
		return
	}
	if cmd.Kind == "stop" || cmd.Kind == "shutdown" {
		err := h.stopLocked()
		if cmd.Kind == "shutdown" {
			h.closed = true
			close(h.shutdown)
		}
		r.State = "completed" // completed stop signal, not completed session
		if err != nil {
			r.State = "failed"
			r.Error = err.Error()
		}
		if err := h.persistResult(r); err != nil {
			h.fault = err.Error()
			r.State = "failed"
			r.Error = "receipt persistence failed"
		}
		h.last = &r
		respond(w, 200, r)
		return
	}
	if cmd.Kind != "capture" && cmd.Kind != "click" && cmd.Kind != "hold" && cmd.Kind != "run-session" {
		reject(fmt.Errorf("unknown command"))
		return
	}
	if h.active != "" {
		reject(fmt.Errorf("host busy: %s", h.state))
		return
	}
	if h.fault != "" {
		reject(fmt.Errorf("host requires recovery: %s", h.fault))
		return
	}
	if cmd.Kind != "capture" {
		if _, err := os.Stat(h.cfg.StopFile); err == nil {
			reject(fmt.Errorf("stop latch exists; operator reconciliation required"))
			return
		} else if !os.IsNotExist(err) {
			reject(err)
			return
		}
	}
	ref := h.ref
	if cmd.Kind == "click" || cmd.Kind == "hold" {
		if !validID(cmd.Reference) || cmd.Reference != h.refID || ref.At.IsZero() || time.Since(ref.At) < 0 || time.Since(ref.At) > desktop.MaxAge {
			reject(fmt.Errorf("fresh server-owned capture required"))
			return
		}
		if cmd.X < 0 || cmd.Y < 0 || int64(cmd.X) >= int64(ref.Window.Client.Right)-int64(ref.Window.Client.Left) || int64(cmd.Y) >= int64(ref.Window.Client.Bottom)-int64(ref.Window.Client.Top) {
			reject(fmt.Errorf("coordinates outside client"))
			return
		}
		if err := writeExclusive(filepath.Join(h.cfg.Root, "captures", cmd.Reference+".input-used"), []byte(cmd.ID)); err != nil {
			reject(fmt.Errorf("capture consumed or cannot be claimed: %w", err))
			return
		}
	}
	h.refID = ""
	h.ref = desktop.Capture{}
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel
	h.done = make(chan struct{})
	h.active = cmd.ID
	h.state = cmd.Kind
	go h.run(ctx, cmd, r, ref)
	respond(w, http.StatusAccepted, r)
}

func (h *Host) run(ctx context.Context, cmd Command, r Receipt, ref desktop.Capture) {
	var err error
	var captured desktop.Capture
	switch cmd.Kind {
	case "capture":
		captured, err = h.backend.Capture(ctx)
		if err == nil {
			var data []byte
			data, err = json.Marshal(captured)
			if err == nil {
				err = writeExclusive(filepath.Join(h.cfg.Root, "captures", cmd.ID+".json"), data)
			}
			if err == nil {
				r.Reference = cmd.ID
			}
		}
	case "click", "hold":
		inputAction := h.backend.Click
		if cmd.Kind == "hold" {
			inputAction = h.backend.Hold
		}
		var input desktop.Receipt
		input, err = inputAction(ctx, ref, cmd.X, cmd.Y, h.cfg.StopFile)
		r.Input = &input
	case "run-session":
		out := filepath.Join(h.cfg.Root, "sessions", cmd.ID)
		err = os.Mkdir(out, 0700)
		if err == nil {
			var f *os.File
			f, err = os.OpenFile(filepath.Join(out, "output.log"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if err == nil {
				log := &boundedLog{file: f, remaining: LogLimit}
				err = h.backend.RunSession(ctx, out, log)
				err = errors.Join(err, log.close())
			}
		}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if err == nil {
		err = ctx.Err()
	}
	r.State = "completed"
	if err != nil {
		r.State = "failed"
		r.Error = err.Error()
	}
	r.At = time.Now().UTC()
	if persistErr := h.persistResult(r); persistErr != nil {
		h.fault = persistErr.Error()
		r.State = "failed"
		r.Error += "; receipt persistence failed"
	}
	if cmd.Kind == "capture" && err == nil && h.state != "stopping" {
		h.refID = cmd.ID
		captured.PNG = nil
		h.ref = captured
	}
	h.last = &r
	h.cancel()
	h.cancel = nil
	h.active = ""
	h.state = "idle"
	close(h.done)
}

// Keep the beginning of each session log, with an explicit truncation marker.
// Full game journals and observations remain under the server-owned session dir.
type boundedLog struct {
	mu        sync.Mutex
	file      *os.File
	remaining int
	err       error
	truncated bool
}

func (b *boundedLog) close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	err := errors.Join(b.err, b.file.Sync(), b.file.Close())
	b.err = os.ErrClosed
	return err
}

func (b *boundedLog) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.err != nil {
		return 0, b.err
	}
	n := len(p)
	if len(p) > b.remaining {
		p = p[:b.remaining]
	}
	if len(p) > 0 {
		_, b.err = b.file.Write(p)
		b.remaining -= len(p)
	}
	if b.err == nil && n > len(p) && !b.truncated {
		_, b.err = b.file.WriteString("\n[log truncated]\n")
		b.truncated = true
	}
	if b.err != nil {
		return 0, b.err
	}
	return n, nil
}
