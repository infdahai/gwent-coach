// cardlookup imports local card databases and builds/queries local art templates.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gwentcoachmvp/internal/cardindex"
	"gwentcoachmvp/internal/cardmatch"
)

type sample struct {
	CardID int           `json:"card_id"`
	Image  string        `json:"image"`
	ROI    cardmatch.ROI `json:"roi"`
}
type manifest struct {
	DataVersion  string   `json:"data_version"`
	RulesVersion string   `json:"rules_version"`
	Samples      []sample `json:"samples"`
}

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("cardlookup", flag.ContinueOnError)
	mode := fs.String("mode", "", "import, templates, card, or image")
	dbPath := fs.String("db", "runs/cards.db", "local bbolt database")
	data := fs.String("data-version", cardindex.DataVersion, "required data version")
	rules := fs.String("rules-version", cardindex.RulesVersion, "required rules version")
	sources := fs.String("sources", "", "comma-separated local snapshot paths (English required)")
	manifestPath := fs.String("manifest", "", "local sample manifest")
	templates := fs.String("templates", "runs/templates.json", "local normalized template index")
	id := fs.Int("id", 0, "card ID")
	language := fs.String("language", "en", "card text language")
	imagePath := fs.String("image", "", "query PNG/JPEG")
	region := fs.String("roi", "", "explicit art ROI x,y,width,height in original image pixels")
	deckArg := fs.String("deck", "", "optional comma-separated known deck IDs; every ID needs a template")
	maxDistance := fs.Int("max-distance", -1, "required image Hamming distance cutoff 0..64 (unvalidated)")
	minGap := fs.Int("min-gap", -1, "required distinct-ID runner-up distance gap 1..64 (unvalidated)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(fs.Args()) != 0 {
		return fmt.Errorf("unexpected positional arguments")
	}
	version := cardindex.Version{Data: *data, Rules: *rules}
	start := time.Now()
	encode := func(v any) error { enc := json.NewEncoder(out); enc.SetIndent("", "  "); return enc.Encode(v) }
	if *mode == "import" {
		if *sources == "" {
			return fmt.Errorf("-sources required")
		}
		if err := cardindex.Import(*dbPath, version, strings.Split(*sources, ",")); err != nil {
			return err
		}
		db, err := cardindex.Open(*dbPath, version)
		if err != nil {
			return err
		}
		return encode(map[string]any{"version": version, "cards": db.Len(), "import_and_validation_ns": time.Since(start).Nanoseconds()})
	}
	if *mode != "templates" && *mode != "card" && *mode != "image" {
		return fmt.Errorf("-mode must be import, templates, card, or image")
	}
	db, err := cardindex.Open(*dbPath, version)
	if err != nil {
		return err
	}
	openNS := time.Since(start).Nanoseconds()
	switch *mode {
	case "card":
		start = time.Now()
		card, handler, supported, found := db.Lookup(*id, *language)
		lookupNS := time.Since(start).Nanoseconds()
		status := "unknown"
		if found {
			status = "found"
		}
		return encode(map[string]any{"status": status, "version": version, "card": card, "handler": handler, "supported": supported, "database_open_ns": openNS, "lookup_ns": lookupNS})
	case "templates":
		var m manifest
		if err := cardindex.ReadJSON(*manifestPath, &m); err != nil {
			return err
		}
		if m.DataVersion != version.Data || m.RulesVersion != version.Rules {
			return fmt.Errorf("sample manifest version mismatch")
		}
		file := cardmatch.File{Version: version, FeatureVersion: cardmatch.FeatureVersion}
		for _, s := range m.Samples {
			path := s.Image
			if !filepath.IsAbs(path) {
				path = filepath.Join(filepath.Dir(*manifestPath), path)
			}
			img, err := readImage(path)
			if err != nil {
				return err
			}
			feature, err := cardmatch.Normalize(img, s.ROI)
			if err != nil {
				return fmt.Errorf("sample %d: %w", s.CardID, err)
			}
			file.Templates = append(file.Templates, cardmatch.Template{CardID: s.CardID, Feature: feature})
		}
		if _, err := cardmatch.New(file, db); err != nil {
			return err
		}
		b, err := json.MarshalIndent(file, "", "  ")
		if err != nil {
			return err
		}
		// Templates may encode third-party art. Never overwrite existing local data.
		f, err := os.OpenFile(*templates, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return err
		}
		_, writeErr := f.Write(b)
		closeErr := f.Close()
		if writeErr != nil {
			return writeErr
		}
		if closeErr != nil {
			return closeErr
		}
		return encode(map[string]any{"templates": len(file.Templates), "calibration": "unvalidated", "build_ns": time.Since(start).Nanoseconds()})
	case "image":
		thresholds := cardmatch.Thresholds{MaxDistance: *maxDistance, MinGap: *minGap}
		if err := thresholds.Validate(); err != nil {
			return err
		}
		var file cardmatch.File
		if err := cardindex.ReadJSON(*templates, &file); err != nil {
			return err
		}
		index, err := cardmatch.New(file, db)
		if err != nil {
			return err
		}
		roi, err := parseROI(*region)
		if err != nil {
			return err
		}
		var deck []int
		deckSet := false
		fs.Visit(func(f *flag.Flag) {
			if f.Name == "deck" {
				deckSet = true
			}
		})
		if deckSet {
			deck = []int{}
			for _, s := range strings.Split(*deckArg, ",") {
				id, err := strconv.Atoi(strings.TrimSpace(s))
				if err != nil || id <= 0 {
					return fmt.Errorf("invalid/empty deck ID %q", s)
				}
				deck = append(deck, id)
			}
		}
		start = time.Now()
		img, err := readImage(*imagePath)
		if err != nil {
			return err
		}
		decodeNS := time.Since(start).Nanoseconds()
		start = time.Now()
		feature, err := cardmatch.Normalize(img, roi)
		if err != nil {
			return err
		}
		normalizeNS := time.Since(start).Nanoseconds()
		start = time.Now()
		result, err := index.Match(feature, deck, thresholds)
		if err != nil {
			return err
		}
		matchNS := time.Since(start).Nanoseconds()
		return encode(map[string]any{"result": result, "roi": roi, "version": version, "database_open_ns": openNS, "decode_ns": decodeNS, "normalize_ns": normalizeNS, "match_ns": matchNS})
	}
	return nil
}

func parseROI(s string) (cardmatch.ROI, error) {
	parts := strings.Split(s, ",")
	if len(parts) != 4 {
		return cardmatch.ROI{}, fmt.Errorf("-roi requires x,y,width,height")
	}
	var n [4]int
	for i, p := range parts {
		v, err := strconv.Atoi(strings.TrimSpace(p))
		if err != nil {
			return cardmatch.ROI{}, err
		}
		n[i] = v
	}
	return cardmatch.ROI{X: n[0], Y: n[1], Width: n[2], Height: n[3]}, nil
}

func readImage(path string) (image.Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	config, _, err := image.DecodeConfig(f)
	if err != nil {
		return nil, err
	}
	if config.Width <= 0 || config.Height <= 0 || config.Width > 16000000/config.Height {
		return nil, fmt.Errorf("image exceeds 16 million pixels")
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	img, _, err := image.Decode(f)
	return img, err
}
