package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// importCollections snapshots complete records from locally accessible collector runs.
func importCollections(source, dest string, out io.Writer) error {
	source, err := filepath.Abs(source)
	if err != nil {
		return err
	}
	dest, err = filepath.Abs(dest)
	if err != nil {
		return err
	}
	for _, path := range []string{source, dest} {
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return fmt.Errorf("%s must be an existing directory", path)
		}
	}
	source, err = filepath.EvalSymlinks(source)
	if err != nil {
		return err
	}
	dest, err = filepath.EvalSymlinks(dest)
	if err != nil {
		return err
	}
	within := func(a, b string) bool {
		rel, err := filepath.Rel(a, b)
		return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
	}
	if within(source, dest) || within(dest, source) {
		return fmt.Errorf("source and destination must be separate directories")
	}
	unlock, err := acquireImportLock(dest)
	if err != nil {
		return fmt.Errorf("acquire import lock: %w", err)
	}
	defer unlock()
	entries, err := os.ReadDir(source)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "readonly-collection-") {
			continue
		}
		path := filepath.Join(source, entry.Name(), "recognition.jsonl")
		info, err := os.Lstat(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("%s is not a regular file", path)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		end := bytes.LastIndexByte(data, '\n')
		data = data[:end+1]
		if len(data) > 0 {
			if _, err := analyze(bytes.NewReader(data)); err != nil {
				return fmt.Errorf("%s: %w", path, err)
			}
		}
		target := filepath.Join(dest, entry.Name()+".jsonl")
		targetInfo, statErr := os.Lstat(target)
		if statErr != nil && !os.IsNotExist(statErr) {
			return statErr
		}
		if statErr == nil && !targetInfo.Mode().IsRegular() {
			return fmt.Errorf("%s is not a regular file", target)
		}
		previous, err := os.ReadFile(target)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if !bytes.HasPrefix(data, previous) {
			return fmt.Errorf("%s: source conflicts with imported records", path)
		}
		if bytes.Equal(data, previous) {
			continue
		}
		tmp, err := os.CreateTemp(dest, ".collection-*")
		if err != nil {
			return err
		}
		name := tmp.Name()
		_, writeErr := tmp.Write(data)
		closeErr := tmp.Close()
		if writeErr != nil {
			os.Remove(name)
			return writeErr
		}
		if closeErr != nil {
			os.Remove(name)
			return closeErr
		}
		if err := os.Rename(name, target); err != nil {
			os.Remove(name)
			return err
		}
		if _, err := fmt.Fprintf(out, "%s: imported %d bytes\n", entry.Name(), len(data)-len(previous)); err != nil {
			return err
		}
	}
	return nil
}
