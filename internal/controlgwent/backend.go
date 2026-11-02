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
	_, err := b.observedConfig()
	return err
}

// Load the observed profile and model bytes together. RunSession uses this same
// validated in-memory snapshot, including the stop latch, throughout the run.
func (b Backend) observedConfig() (*observe.ObservedMatchConfig, error) {
	if b.SessionConfig == "" {
		return nil, nil
	}
	data, err := os.ReadFile(b.SessionConfig)
	if err != nil {
		return nil, err
	}
	var header struct{ Kind, LoopConfig string }
	if err = json.Unmarshal(data, &header); err != nil {
		return nil, err
	}
	var observed *observe.ObservedMatchConfig
	var stop string
	if header.Kind == observe.ObservedMatchKind {
		cfg, err := observe.LoadObservedMatchConfig(b.SessionConfig)
		if err != nil {
			return nil, err
		}
		observed, stop = &cfg, cfg.StopFile
	} else {
		if header.Kind != "" {
			return nil, fmt.Errorf("unknown session kind %q", header.Kind)
		}
		path := header.LoopConfig
		if !filepath.IsAbs(path) {
			path = filepath.Join(filepath.Dir(b.SessionConfig), path)
		}
		data, err = os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		var loop observe.NativeLoopConfig
		if err = json.Unmarshal(data, &loop); err != nil {
			return nil, err
		}
		stop = loop.StopFile
	}
	if !filepath.IsAbs(stop) || !strings.EqualFold(filepath.Clean(stop), filepath.Clean(b.StopFile)) {
		return nil, fmt.Errorf("host stop file must match configured session's absolute StopFile")
	}
	return observed, nil
}

func (b Backend) RunSession(ctx context.Context, out string, output io.Writer) error {
	if b.SessionConfig == "" {
		return fmt.Errorf("host has no configured session")
	}
	cfg, err := b.observedConfig()
	if err != nil {
		return err
	}
	logger := log.New(output, "", log.LstdFlags|log.LUTC)
	if cfg != nil {
		return observe.RunObservedMatch(ctx, out, logger, *cfg, output)
	}
	return observe.RunNativeSessionContext(ctx, out, logger, b.SessionConfig, output)
}
