package control

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"gwentcoachmvp/internal/desktop"
)

type Client struct {
	base, token string
	http        *http.Client
}

func NewClient(base, token string) (*Client, error) {
	if err := ValidateURL(base); err != nil {
		return nil, err
	}
	if len(token) < 32 || strings.ContainsAny(token, " \t\n\r") {
		return nil, fmt.Errorf("invalid control token")
	}
	return &Client{base: strings.TrimRight(base, "/"), token: token, http: &http.Client{
		Timeout:       20 * time.Second,
		Transport:     &http.Transport{Proxy: nil},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}, nil
}

func (c *Client) call(ctx context.Context, method, path string, body, result any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		// A streaming reader prevents net/http from replaying mutations. No
		// Idempotency-Key header or redirect/retry policy is used.
		reader = struct{ io.Reader }{bytes.NewReader(data)}
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("transport outcome unknown; inspect receipt/status, do not replay: %w", err)
	}
	defer resp.Body.Close()
	if err = json.NewDecoder(io.LimitReader(resp.Body, 64<<20)).Decode(result); err != nil {
		return fmt.Errorf("response unreadable; inspect receipt/status, do not replay: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("control HTTP %d (inspect receipt error)", resp.StatusCode)
	}
	return nil
}

func (c *Client) Submit(ctx context.Context, cmd Command) (Receipt, error) {
	var r Receipt
	err := c.call(ctx, "POST", "/v1/commands", cmd, &r)
	return r, err
}
func (c *Client) Status(ctx context.Context) (Status, error) {
	var s Status
	err := c.call(ctx, "GET", "/v1/status", nil, &s)
	return s, err
}
func (c *Client) Receipt(ctx context.Context, id string) (Receipt, error) {
	var r Receipt
	if !validID(id) {
		return r, fmt.Errorf("invalid request ID")
	}
	err := c.call(ctx, "GET", "/v1/receipts/"+id, nil, &r)
	return r, err
}
func (c *Client) Capture(ctx context.Context, id string) (desktop.Capture, error) {
	var r desktop.Capture
	if !validID(id) {
		return r, fmt.Errorf("invalid capture ID")
	}
	err := c.call(ctx, "GET", "/v1/captures/"+id, nil, &r)
	return r, err
}
func (c *Client) Logs(ctx context.Context, id string) (string, error) {
	var r struct {
		Log string `json:"log"`
	}
	if !validID(id) {
		return "", fmt.Errorf("invalid request ID")
	}
	err := c.call(ctx, "GET", "/v1/logs/"+id, nil, &r)
	return r.Log, err
}

// Wait only polls an existing receipt. It never resubmits a command.
func (c *Client) Wait(ctx context.Context, id string) (Receipt, error) {
	for {
		r, err := c.Receipt(ctx, id)
		if err != nil || r.State != "accepted" {
			return r, err
		}
		select {
		case <-ctx.Done():
			return r, fmt.Errorf("request %s still unresolved; inspect receipt, do not replay: %w", id, ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
}
