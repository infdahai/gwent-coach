package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gwentcoachmvp/internal/desktop"
)

const testToken = "0123456789abcdef0123456789abcdef"

type fakeBackend struct {
	clicks  atomic.Int32
	holds   atomic.Int32
	runs    atomic.Int32
	capture func(context.Context) (desktop.Capture, error)
	click   func(context.Context) (desktop.Receipt, error)
	run     func(context.Context, io.Writer) error
}

func (f *fakeBackend) Capture(ctx context.Context) (desktop.Capture, error) {
	if f.capture != nil {
		return f.capture(ctx)
	}
	return desktop.Capture{At: time.Now(), Window: desktop.Window{Handle: 1, Client: desktop.Rect{Right: 100, Bottom: 100}}, PNG: []byte("fake PNG")}, nil
}
func (f *fakeBackend) Click(ctx context.Context, _ desktop.Capture, _, _ int, _ string) (desktop.Receipt, error) {
	f.clicks.Add(1)
	if f.click != nil {
		return f.click(ctx)
	}
	return desktop.Receipt{Outcome: "delivered_unconfirmed", EventsSent: 2}, nil
}
func (f *fakeBackend) Hold(ctx context.Context, _ desktop.Capture, _, _ int, _ string) (desktop.Receipt, error) {
	f.holds.Add(1)
	if f.click != nil {
		return f.click(ctx)
	}
	return desktop.Receipt{Outcome: "delivered_unconfirmed", EventsSent: 2}, nil
}
func (f *fakeBackend) RunSession(ctx context.Context, _ string, w io.Writer) error {
	f.runs.Add(1)
	if f.run != nil {
		return f.run(ctx, w)
	}
	return nil
}

func setup(t *testing.T, backend *fakeBackend) (*Host, *Client) {
	t.Helper()
	dir := t.TempDir()
	h, err := NewHost(Config{Token: testToken, Root: filepath.Join(dir, "state"), StopFile: filepath.Join(dir, "STOP")}, backend)
	if err != nil {
		t.Fatal(err)
	}
	s := httptest.NewServer(h)
	c, err := NewClient(s.URL, testToken)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		s.Close()
		if err := h.Close(); err != nil {
			t.Error(err)
		}
	})
	return h, c
}
func submit(t *testing.T, c *Client, kind string) Receipt {
	t.Helper()
	r, err := c.Submit(context.Background(), Command{ID: NewID(), Kind: kind})
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func await(t *testing.T, c *Client, id string) Receipt {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	r, err := c.Wait(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func capture(t *testing.T, c *Client) Receipt {
	t.Helper()
	return await(t, c, submit(t, c, "capture").ID)
}

func TestAuthenticationAndBrowserIsolation(t *testing.T) {
	h, _ := setup(t, &fakeBackend{})
	for _, tc := range []struct {
		name, token, origin, fetch, host string
		want                             int
	}{
		{"missing", "", "", "", "127.0.0.1:17391", 401},
		{"wrong", "wrong", "", "", "127.0.0.1:17391", 401},
		{"origin", testToken, "http://example.test", "", "127.0.0.1:17391", 403},
		{"null-origin", testToken, "null", "", "127.0.0.1:17391", 403},
		{"fetch", testToken, "", "same-origin", "127.0.0.1:17391", 403},
		{"dns-rebinding", testToken, "", "", "example.test:17391", 403},
		{"authenticated", testToken, "", "", "127.0.0.1:17391", 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "http://127.0.0.1:17391/v1/status", nil)
			r.Host = tc.host
			if tc.token != "" {
				r.Header.Set("Authorization", "Bearer "+tc.token)
			}
			r.Header.Set("Origin", tc.origin)
			r.Header.Set("Sec-Fetch-Site", tc.fetch)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			if strings.Contains(w.Body.String(), testToken) {
				t.Fatal("token leaked")
			}
		})
	}
}

func TestLoopbackOnly(t *testing.T) {
	for _, address := range []string{"0.0.0.0:17391", ":17391", "localhost:17391", "[::]:17391", "192.0.2.1:17391"} {
		if ValidateAddress(address) == nil {
			t.Fatalf("allowed %s", address)
		}
	}
	for _, address := range []string{"127.0.0.1:17391", "[::1]:17391"} {
		if err := ValidateAddress(address); err != nil {
			t.Fatal(err)
		}
	}
	for _, url := range []string{"https://127.0.0.1:17391", "http://localhost:17391", "http://user@127.0.0.1:17391", "http://127.0.0.1:17391/path"} {
		if _, err := NewClient(url, testToken); err == nil {
			t.Fatalf("allowed %s", url)
		}
	}
}

func TestSessionExcludesManualInputAndStopWaitsForReturn(t *testing.T) {
	started, cancelled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	backend := &fakeBackend{run: func(ctx context.Context, w io.Writer) error {
		close(started)
		<-ctx.Done()
		close(cancelled)
		<-release
		return ctx.Err()
	}}
	h, c := setup(t, backend)
	// Ensure cleanup cannot block even when an assertion fails.
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	ref := capture(t, c)
	run := submit(t, c, "run-session")
	<-started
	for _, cmd := range []Command{{ID: NewID(), Kind: "click", Reference: ref.Reference, X: 1, Y: 1}, {ID: NewID(), Kind: "run-session"}, {ID: NewID(), Kind: "capture"}} {
		r, err := c.Submit(context.Background(), cmd)
		if err == nil || r.State != "rejected" {
			t.Fatalf("busy accepted %+v %v", r, err)
		}
	}
	stop := submit(t, c, "stop")
	if stop.State != "completed" {
		t.Fatalf("stop %+v", stop)
	}
	<-cancelled
	s := h.Status()
	if s.State != "stopping" || s.ActiveID != run.ID || !s.StopSignaled {
		t.Fatalf("prematurely stopped: %+v", s)
	}
	if _, err := os.Stat(h.cfg.StopFile); err != nil {
		t.Fatal(err)
	}
	r, err := c.Receipt(context.Background(), run.ID)
	if err != nil || r.State != "accepted" {
		t.Fatalf("premature receipt %+v %v", r, err)
	}
	once.Do(func() { close(release) })
	r = await(t, c, run.ID)
	if r.State != "failed" || !strings.Contains(r.Error, "canceled") || h.Status().State != "idle" {
		t.Fatalf("completion %+v", r)
	}
	if backend.clicks.Load() != 0 || backend.runs.Load() != 1 {
		t.Fatal("unexpected input or session")
	}
	if _, err = c.Submit(context.Background(), Command{ID: NewID(), Kind: "run-session"}); err == nil {
		t.Fatal("stop latch ignored")
	}
}

func TestClickIsSingleUseAndDuplicateNeverExecutes(t *testing.T) {
	b := &fakeBackend{}
	h, c := setup(t, b)
	ref := capture(t, c)
	cmd := Command{ID: NewID(), Kind: "click", Reference: ref.Reference, X: 5, Y: 6}
	if _, err := c.Submit(context.Background(), cmd); err != nil {
		t.Fatal(err)
	}
	r := await(t, c, cmd.ID)
	if r.Input == nil || r.Input.Outcome != "delivered_unconfirmed" {
		t.Fatalf("receipt %+v", r)
	}
	// Changing the body under an existing ID also cannot execute anything.
	cmd.Kind = "run-session"
	duplicate, err := c.Submit(context.Background(), cmd)
	if err == nil || duplicate.Command != "click" || duplicate.State != "completed" {
		t.Fatalf("duplicate %+v %v", duplicate, err)
	}
	cmd.ID, cmd.Kind = NewID(), "click"
	if _, err := c.Submit(context.Background(), cmd); err == nil {
		t.Fatal("reused capture")
	}
	if b.clicks.Load() != 1 || b.runs.Load() != 0 {
		t.Fatal("duplicate input")
	}
	if _, err := os.Stat(filepath.Join(h.cfg.Root, "captures", ref.Reference+".input-used")); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentClicksSerialize(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	b := &fakeBackend{click: func(ctx context.Context) (desktop.Receipt, error) {
		close(started)
		select {
		case <-release:
		case <-ctx.Done():
		}
		return desktop.Receipt{Outcome: "cursor_moved"}, ctx.Err()
	}}
	_, c := setup(t, b)
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	ref := capture(t, c)
	first, err := c.Submit(context.Background(), Command{ID: NewID(), Kind: "click", Reference: ref.Reference, X: 1, Y: 1})
	if err != nil {
		t.Fatal(err)
	}
	<-started
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := c.Submit(context.Background(), Command{ID: NewID(), Kind: "click", Reference: ref.Reference, X: 2, Y: 2})
			if err == nil || r.State != "rejected" {
				t.Errorf("concurrent click %+v %v", r, err)
			}
		}()
	}
	wg.Wait()
	once.Do(func() { close(release) })
	await(t, c, first.ID)
	if b.clicks.Load() != 1 {
		t.Fatal("concurrent input")
	}
}

func TestCaptureFreshnessAndBounds(t *testing.T) {
	for _, tc := range []struct {
		name string
		age  time.Duration
		x, y int
	}{
		{"expired", time.Minute, 1, 1}, {"future", -time.Minute, 1, 1}, {"negative", 0, -1, 1}, {"right-edge", 0, 100, 1}, {"bottom-edge", 0, 1, 100},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := &fakeBackend{capture: func(context.Context) (desktop.Capture, error) {
				return desktop.Capture{At: time.Now().Add(-tc.age), Window: desktop.Window{Client: desktop.Rect{Right: 100, Bottom: 100}}}, nil
			}}
			_, c := setup(t, b)
			ref := capture(t, c)
			if _, err := c.Submit(context.Background(), Command{ID: NewID(), Kind: "click", Reference: ref.Reference, X: tc.x, Y: tc.y}); err == nil {
				t.Fatal("invalid capture input accepted")
			}
			if b.clicks.Load() != 0 {
				t.Fatal("invalid input reached backend")
			}
		})
	}
}

func TestErrorReceiptPreservesPartialDelivery(t *testing.T) {
	b := &fakeBackend{click: func(context.Context) (desktop.Receipt, error) {
		return desktop.Receipt{Outcome: "partial_or_unknown", EventsSent: 1}, errors.New("delivery failed")
	}}
	_, c := setup(t, b)
	ref := capture(t, c)
	r, err := c.Submit(context.Background(), Command{ID: NewID(), Kind: "click", Reference: ref.Reference, X: 2, Y: 3})
	if err != nil {
		t.Fatal(err)
	}
	r = await(t, c, r.ID)
	if r.State != "failed" || r.Input.EventsSent != 1 || r.Input.Outcome != "partial_or_unknown" || r.Error != "delivery failed" {
		t.Fatalf("lost outcome %+v", r)
	}
}

func TestRequestClaimsSurviveRestart(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{Token: testToken, Root: dir, StopFile: filepath.Join(dir, "STOP")}
	b := &fakeBackend{}
	h, err := NewHost(cfg, b)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewHost(cfg, b); err == nil {
		t.Fatal("second owner accepted")
	}
	s := httptest.NewServer(h)
	c, _ := NewClient(s.URL, testToken)
	r := await(t, c, submit(t, c, "run-session").ID)
	// A claim without a result represents a crash/ambiguous completion.
	uncertain := Receipt{ID: NewID(), Command: "click", State: "accepted"}
	data, _ := json.Marshal(uncertain)
	if err := writeExclusive(h.requestPath(uncertain.ID, ".claim.json"), data); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if err := h.Close(); err != nil {
		t.Fatal(err)
	}
	h, err = NewHost(cfg, b)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	s = httptest.NewServer(h)
	defer s.Close()
	c, _ = NewClient(s.URL, testToken)
	for _, id := range []string{r.ID, uncertain.ID} {
		if _, err = c.Submit(context.Background(), Command{ID: id, Kind: "run-session"}); err == nil {
			t.Fatal("replayed persisted ID")
		}
	}
	if b.runs.Load() != 1 {
		t.Fatal("restart replayed command")
	}
}

func TestLogsBoundedAndPathsConfined(t *testing.T) {
	_, c := setup(t, &fakeBackend{run: func(_ context.Context, w io.Writer) error {
		_, err := io.WriteString(w, strings.Repeat("x", LogLimit*2))
		return err
	}})
	r := await(t, c, submit(t, c, "run-session").ID)
	text, err := c.Logs(context.Background(), r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(text) > LogLimit+32 || !strings.HasSuffix(text, "[log truncated]\n") {
		t.Fatalf("bad truncation length=%d", len(text))
	}
	for _, path := range []string{"/v1/captures/../../token", "/v1/logs/%2e%2e%2fhost.lock", "/v1/receipts/host.lock", "/token"} {
		req, _ := http.NewRequest("GET", c.base+path, nil)
		req.Header.Set("Authorization", "Bearer "+testToken)
		resp, err := c.http.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode == 200 {
			t.Fatalf("path exposed %s", path)
		}
	}
}

func TestClientNeverReplaysAmbiguousCommand(t *testing.T) {
	var calls atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		conn.Close()
	}))
	defer s.Close()
	c, _ := NewClient(s.URL, testToken)
	_, err := c.Submit(context.Background(), Command{ID: NewID(), Kind: "click"})
	if err == nil || !strings.Contains(err.Error(), "do not replay") || calls.Load() != 1 {
		t.Fatalf("calls=%d err=%v", calls.Load(), err)
	}
}

func TestShutdownSignalsAndClosesAdmission(t *testing.T) {
	h, c := setup(t, &fakeBackend{})
	submit(t, c, "shutdown")
	select {
	case <-h.ShutdownRequested():
	default:
		t.Fatal("shutdown not signaled")
	}
	if !h.Status().StopSignaled {
		t.Fatal("shutdown not durable")
	}
	if _, err := c.Submit(context.Background(), Command{ID: NewID(), Kind: "capture"}); err == nil {
		t.Fatal("accepted after shutdown")
	}
}

func TestStopPersistenceFailureStillCancels(t *testing.T) {
	started, cancelled := make(chan struct{}), make(chan struct{})
	h, c := setup(t, &fakeBackend{run: func(ctx context.Context, _ io.Writer) error {
		close(started)
		<-ctx.Done()
		close(cancelled)
		return ctx.Err()
	}})
	r := submit(t, c, "run-session")
	<-started
	// Parent is a file, making stop persistence impossible on all test platforms.
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	h.mu.Lock()
	h.cfg.StopFile = filepath.Join(blocker, "STOP")
	h.mu.Unlock()
	stop := submit(t, c, "stop")
	if stop.State != "failed" {
		t.Fatalf("hidden stop error %+v", stop)
	}
	<-cancelled
	await(t, c, r.ID)
	if h.Status().Error == "" {
		t.Fatal("missing persistence fault")
	}
}

func ExampleCommand() {
	cmd := Command{ID: "0123456789abcdef0123456789abcdef", Kind: "click", Reference: "fedcba9876543210fedcba9876543210", X: 40, Y: 50}
	data, _ := json.Marshal(cmd)
	fmt.Println(string(data))
	// Output: {"id":"0123456789abcdef0123456789abcdef","kind":"click","reference":"fedcba9876543210fedcba9876543210","x":40,"y":50}
}
