//go:build !darwin && !linux

package main

import "fmt"

func acquireImportLock(dest string) (func(), error) {
	return nil, fmt.Errorf("collection import is supported on macOS and Linux only")
}
