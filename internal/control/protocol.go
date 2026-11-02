// Package control implements the loopback-only resident control protocol. It has
// no game dependency; the configured session runner is supplied by the caller.
package control

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/url"
	"strings"
	"time"

	"gwentcoachmvp/internal/desktop"
)

const DefaultAddress = "127.0.0.1:17391"
const LogLimit = 256 << 10

type Backend interface {
	Capture(context.Context) (desktop.Capture, error)
	Click(context.Context, desktop.Capture, int, int, string) (desktop.Receipt, error)
	Hold(context.Context, desktop.Capture, int, int, string) (desktop.Receipt, error)
	RunSession(context.Context, string, io.Writer) error
}

type Command struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Reference string `json:"reference,omitempty"`
	X         int    `json:"x,omitempty"`
	Y         int    `json:"y,omitempty"`
}

type Receipt struct {
	ID        string           `json:"id"`
	Command   string           `json:"command"`
	State     string           `json:"state"` // accepted, completed, failed, rejected
	Reference string           `json:"reference,omitempty"`
	Input     *desktop.Receipt `json:"input,omitempty"`
	Error     string           `json:"error,omitempty"`
	At        time.Time        `json:"at"`
}

type Status struct {
	State        string   `json:"state"` // idle, capture, click, hold, run-session, stopping
	ActiveID     string   `json:"active_id,omitempty"`
	StopSignaled bool     `json:"stop_signaled"`
	Error        string   `json:"error,omitempty"`
	Last         *Receipt `json:"last,omitempty"`
}

func NewID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}

func validID(id string) bool {
	b, err := hex.DecodeString(id)
	return err == nil && len(b) == 16 && id == strings.ToLower(id)
}

func ValidateAddress(address string) error {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() || port == "" {
		return fmt.Errorf("explicit loopback IP and port required")
	}
	return nil
}

func ValidateURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if u.Scheme != "http" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("plain loopback HTTP base URL required")
	}
	return ValidateAddress(u.Host)
}
