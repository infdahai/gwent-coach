package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"gwentcoachmvp/internal/control"
	"gwentcoachmvp/internal/controlgwent"
)

func runControl(args []string, output, diagnostics io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: coach control <host|status|capture|click|run-session|stop|receipt|logs|shutdown> [flags]")
	}
	command := args[0]
	flags := flag.NewFlagSet("control "+command, flag.ContinueOnError)
	flags.SetOutput(diagnostics)
	address := flags.String("address", control.DefaultAddress, "host explicit loopback IP:port")
	url := flags.String("url", "http://"+control.DefaultAddress, "client loopback URL (SSH forwarded port)")
	tokenFile := flags.String("token-file", "", "file containing authentication token; never passed in URL")
	root := flags.String("state", "", "host-owned persistent state directory")
	stop := flags.String("stop-file", "", "host durable stop latch; must match configured session StopFile")
	profile := flags.String("session-config", "", "host-only pinned native session configuration")
	id := flags.String("id", "", "unique command ID, or existing receipt/log ID")
	reference := flags.String("reference", "", "server capture ID for click")
	x := flags.Int("x", -1, "client pixel x")
	y := flags.Int("y", -1, "client pixel y")
	imagePath := flags.String("image", "", "local PNG output path for capture (new file)")
	wait := flags.Duration("wait", 20*time.Second, "capture/click receipt wait budget; timeout never retries input")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments")
	}
	if *tokenFile == "" {
		return fmt.Errorf("-token-file required")
	}
	token, err := os.ReadFile(*tokenFile)
	if err != nil {
		return err
	}
	secret := strings.TrimSpace(string(token))
	if command == "host" {
		return serveControl(*address, secret, *root, *stop, *profile, diagnostics)
	}
	client, err := control.NewClient(*url, secret)
	if err != nil {
		return err
	}
	ctx := context.Background()
	encode := func(v any) error { return json.NewEncoder(output).Encode(v) }
	switch command {
	case "status":
		s, err := client.Status(ctx)
		if err != nil {
			return err
		}
		return encode(s)
	case "receipt":
		r, err := client.Receipt(ctx, *id)
		if err != nil {
			return err
		}
		return encode(r)
	case "logs":
		text, err := client.Logs(ctx, *id)
		if err != nil {
			return err
		}
		_, err = io.WriteString(output, text)
		return err
	case "capture", "click", "run-session", "stop", "shutdown":
	default:
		return fmt.Errorf("unknown control command %q", command)
	}
	if command == "click" && (*reference == "" || *x < 0 || *y < 0) {
		return fmt.Errorf("click requires -reference, -x and -y")
	}
	if command == "capture" && *imagePath == "" {
		return fmt.Errorf("capture requires -image for local output")
	}
	if command == "capture" {
		if _, err := os.Stat(*imagePath); err == nil {
			return fmt.Errorf("image output exists")
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	if *id == "" {
		*id = control.NewID()
	}
	fmt.Fprintf(diagnostics, "request_id=%s\n", *id)
	r, err := client.Submit(ctx, control.Command{ID: *id, Kind: command, Reference: *reference, X: *x, Y: *y})
	if err != nil {
		if r.ID != "" {
			_ = encode(r)
		}
		return err
	}
	if command == "capture" || command == "click" {
		waitCtx, cancel := context.WithTimeout(ctx, *wait)
		defer cancel()
		r, err = client.Wait(waitCtx, *id)
		if err != nil {
			return err
		}
	}
	if err := encode(r); err != nil {
		return err
	}
	if r.State == "failed" || r.State == "rejected" {
		return fmt.Errorf("command %s: %s", r.State, r.Error)
	}
	if command == "capture" {
		capture, err := client.Capture(ctx, r.Reference)
		if err != nil {
			return err
		}
		f, err := os.OpenFile(*imagePath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return err
		}
		_, err = f.Write(capture.PNG)
		if err = errors.Join(err, f.Close()); err != nil {
			return err
		}
		capture.PNG = nil
		return encode(capture)
	}
	return nil
}

func serveControl(address, token, root, stop, profile string, output io.Writer) error {
	if runtime.GOOS != "windows" {
		return fmt.Errorf("resident host requires Windows; client commands work on Mac and Windows")
	}
	if err := control.ValidateAddress(address); err != nil {
		return err
	}
	if root == "" || stop == "" {
		return fmt.Errorf("host requires -state and -stop-file")
	}
	var err error
	stop, err = filepath.Abs(stop)
	if err != nil {
		return err
	}
	if profile != "" {
		profile, err = filepath.Abs(profile)
		if err != nil {
			return err
		}
	}
	backend := controlgwent.Backend{SessionConfig: profile, StopFile: stop}
	if err = backend.Validate(); err != nil {
		return err
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}
	defer listener.Close()
	host, err := control.NewHost(control.Config{Token: token, Root: root, StopFile: stop}, backend)
	if err != nil {
		return err
	}
	server := &http.Server{Handler: host, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 8192}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	ended := make(chan error, 1)
	go func() { ended <- server.Serve(listener) }()
	fmt.Fprintf(output, "resident control listening on %s (pid %d)\n", listener.Addr(), os.Getpid())
	select {
	case <-ctx.Done():
	case <-host.ShutdownRequested():
	case err = <-ended:
	}
	closeErr := host.Close()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	shutdownErr := server.Shutdown(shutdownCtx)
	if errors.Is(err, http.ErrServerClosed) {
		err = nil
	}
	return errors.Join(err, closeErr, shutdownErr)
}
