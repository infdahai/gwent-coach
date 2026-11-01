// Command hand-scan-offline runs the existing calibrated scanner on saved images.
package main

import (
	"encoding/json"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"os"
	"path/filepath"

	"gwentcoachmvp/internal/cardindex"
	"gwentcoachmvp/internal/cardmatch"
	"gwentcoachmvp/internal/observe"
)

func run(args []string, out io.Writer) error {
	if len(args) != 5 {
		return fmt.Errorf("usage: hand-scan-offline config.json image1 report1 image2 report2")
	}
	var cfg observe.HandScanConfig
	if err := cardindex.ReadJSON(args[0], &cfg); err != nil {
		return err
	}
	resolve := func(p string) string {
		if filepath.IsAbs(p) {
			return p
		}
		return filepath.Join(filepath.Dir(args[0]), p)
	}
	version := cardindex.Version{Data: cardindex.DataVersion, Rules: cardindex.RulesVersion}
	db, err := cardindex.Open(resolve(cfg.Database), version)
	if err != nil {
		return err
	}
	var templates cardmatch.File
	if err = cardindex.ReadJSON(resolve(cfg.Templates), &templates); err != nil {
		return err
	}
	matcher, err := cardmatch.New(templates, db)
	if err != nil {
		return err
	}
	var images [2]image.Image
	for i, p := range []string{args[1], args[3]} {
		f, e := os.Open(p)
		if e != nil {
			return e
		}
		images[i], _, e = image.Decode(f)
		f.Close()
		if e != nil {
			return e
		}
	}
	if args[2] == "" || args[4] == "" {
		return fmt.Errorf("report references required")
	}
	cards, err := observe.ScanHand(cfg, matcher, images)
	if err != nil {
		return err
	}
	return json.NewEncoder(out).Encode(struct {
		Status  string                  `json:"status"`
		Reports [2]string               `json:"reports"`
		Cards   []observe.HandCandidate `json:"cards"`
	}{"unvalidated_candidates_no_input", [2]string{args[2], args[4]}, cards})
}
func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
