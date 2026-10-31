package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"gwentcoachmvp/internal/observe"
)

func main() {
	out := flag.String("out", "runs", "directory for timestamped observation reports")
	action := flag.String("action", "", "single reviewed input: click, hold (1.5s), or escape (default observe only)")
	x := flag.Int("x", 0, "click x in client pixels")
	y := flag.Int("y", 0, "click y in client pixels")
	reference := flag.String("reference", "", "recent successful observation report.json")
	stopFile := flag.String("stop-file", "", "existing file pauses input; also hold F8 or Ctrl")
	flag.Parse()
	if err := os.MkdirAll(*out, 0755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	f, err := os.OpenFile(filepath.Join(*out, "coach.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	logger := log.New(f, "", log.LstdFlags|log.LUTC)
	logger.Printf("starting pid=%d action=%q", os.Getpid(), *action)
	var runErr error
	if *action == "" {
		runErr = observe.Run(*out, logger)
	} else {
		runErr = observe.Execute(*out, logger, observe.Action{Kind: *action, X: *x, Y: *y, Reference: *reference, StopFile: *stopFile})
	}
	if err := runErr; err != nil {
		logger.Printf("run failed: %v", err)
		fmt.Fprintln(os.Stderr, err)
		f.Close()
		os.Exit(1)
	}
	logger.Print("observation complete")
	f.Close()
}
