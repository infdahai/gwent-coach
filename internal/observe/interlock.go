package observe

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
)

// acquireInterlock is a durable, exclusive claim. It is intentionally never
// removed by runtime code, including on error or process termination.
func acquireInterlock(root string) error {
	if root == "" {
		return fmt.Errorf("missing interlock root")
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(root, "navigation.claim"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("navigation interlock held or unavailable: %w", err)
	}
	if _, err = f.WriteString("Navigation claimed; inspect all action records before manual reconciliation.\n"); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// loadBoundPhases binds the bytes actually parsed to an explicit digest. The
// digest establishes identity, not classifier correctness or permission to act.
func loadBoundPhases(path, expected string) (*PhaseModel, error) {
	if len(expected) != 64 {
		return nil, fmt.Errorf("expected phase model SHA-256 required")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(b)
	if hex.EncodeToString(digest[:]) != expected {
		return nil, fmt.Errorf("phase model digest mismatch")
	}
	return parsePhases(b)
}
