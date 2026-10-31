package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"gwentcoachmvp/internal/cards"
)

func main() {
	path := flag.String("db", "data/cards/14.10.0/cn/cards.json", "cached gwent.one snapshot")
	query := flag.String("query", "", "card ID or name fragment; empty lists all")
	flag.Parse()
	c, err := cards.Load(*path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	matches := c.Find(*query)
	result := struct {
		Version      string       `json:"version"`
		Language     string       `json:"language"`
		CatalogCount int          `json:"catalog_count"`
		Matches      []cards.Card `json:"matches"`
	}{c.Version, c.Language, len(c.Cards), matches}
	e := json.NewEncoder(os.Stdout)
	e.SetIndent("", "  ")
	if err := e.Encode(result); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
