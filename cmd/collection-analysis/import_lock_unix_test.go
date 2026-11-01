//go:build darwin || linux

package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestImportLockSurvivesProcessDeath(t *testing.T) {
	if dest := os.Getenv("COLLECTION_LOCK_TEST_DIR"); dest != "" {
		unlock, err := acquireImportLock(dest)
		if err != nil {
			os.Exit(2)
		}
		defer unlock()
		fmt.Println("locked")
		select {}
	}
	dest := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestImportLockSurvivesProcessDeath$")
	cmd.Env = append(os.Environ(), "COLLECTION_LOCK_TEST_DIR="+dest)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if cmd.ProcessState == nil {
			cmd.Process.Kill()
			cmd.Wait()
		}
	}()
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || line != "locked\n" {
		t.Fatalf("child did not lock: %q %v", line, err)
	}
	if unlock, err := acquireImportLock(dest); err == nil {
		unlock()
		t.Fatal("concurrent lock succeeded")
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err == nil {
		t.Fatal("expected killed process")
	}
	unlock, err := acquireImportLock(dest)
	if err != nil {
		t.Fatalf("lock not released after process death: %v", err)
	}
	unlock()
	if _, err := os.Stat(filepath.Join(dest, ".collection-import.lock")); err != nil {
		t.Fatal(err)
	}
}
