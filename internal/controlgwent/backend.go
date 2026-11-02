// Package controlgwent connects the generic resident host to the existing
// configured Gwent session. Clients cannot submit executable paths or profiles.
package controlgwent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"

	"gwentcoachmvp/internal/desktop"
	"gwentcoachmvp/internal/observe"
)

type Backend struct {
	SessionConfig string
	StopFile      string
}

func (b Backend) Capture(ctx context.Context) (desktop.Capture, error) { return desktop.Take(ctx) }
func (b Backend) Click(ctx context.Context, ref desktop.Capture, x, y int, stop string) (desktop.Receipt, error) {
	return desktop.Click(ctx, ref, x, y, stop)
}

func (b Backend) Validate() error {
	if b.SessionConfig == "" {
		return nil
	}
	data, err := os.ReadFile(b.SessionConfig)
	if err != nil {
		return err
	}
	var cfg observe.NativeSessionConfig
	if err = json.Unmarshal(data, &cfg); err != nil {
		return err
	}
	path := cfg.LoopConfig
	if !filepath.IsAbs(path) {
		path = filepath.Join(filepath.Dir(b.SessionConfig), path)
	}
	data, err = os.ReadFile(path)
	if err != nil {
		return err
	}
	var loop observe.NativeLoopConfig
	if err = json.Unmarshal(data, &loop); err != nil {
		return err
	}
	// Existing native input interprets StopFile as an absolute path. Refuse a
	// mismatch instead of running with a second, ineffective stop latch.
	if !filepath.IsAbs(loop.StopFile) || !strings.EqualFold(filepath.Clean(loop.StopFile), filepath.Clean(b.StopFile)) {
		return fmt.Errorf("host stop file must match configured native loop's absolute StopFile")
	}
	return nil
}

func (b Backend) RunSession(ctx context.Context, out string, output io.Writer) error {
	if b.SessionConfig == "" {
		return fmt.Errorf("host has no configured session")
	}
	if err := b.Validate(); err != nil {
		return err
	}
	return observe.RunNativeSessionContext(ctx, out, log.New(output, "", log.LstdFlags|log.LUTC), b.SessionConfig, output)
}
