// trainworker imports sealed local match directories without controlling the game.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"time"

	"gwentcoachmvp/internal/training"
)

func main() {
	var c training.Config
	flag.StringVar(&c.Inbox, "inbox", "", "local directory of sealed match directories")
	flag.StringVar(&c.Store, "store", "", "private worker state directory")
	flag.StringVar(&c.RulesVersion, "rules-version", "native-v1", "rules version")
	flag.StringVar(&c.CatalogVersion, "catalog-version", "14.10.0", "catalog version")
	flag.StringVar(&c.PolicyVersion, "policy-version", "action-ranker-v1", "candidate policy version")
	interval := flag.Duration("watch", 0, "poll interval; zero runs once")
	flag.Parse()
	if *interval < 0 {
		fmt.Fprintln(os.Stderr, "watch interval must be nonnegative")
		os.Exit(2)
	}
	run := func() error {
		reports, err := training.Run(c)
		enc := json.NewEncoder(os.Stdout)
		if len(reports) == 0 && err == nil {
			if e := enc.Encode(map[string]string{"status": "empty", "detail": "no match directories found"}); e != nil {
				return e
			}
		}
		for _, r := range reports {
			if e := enc.Encode(r); e != nil {
				return e
			}
		}
		return err
	}
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		if *interval == 0 {
			os.Exit(1)
		}
	}
	if *interval == 0 {
		return
	}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt)
	defer signal.Stop(signals)
	ticker := time.NewTicker(*interval)
	defer ticker.Stop()
	for {
		select {
		case <-signals:
			return
		case <-ticker.C:
			if err := run(); err != nil {
				fmt.Fprintln(os.Stderr, err)
			}
		}
	}
}
