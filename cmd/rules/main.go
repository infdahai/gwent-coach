package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"gwentcoachmvp/internal/cards"
	"gwentcoachmvp/internal/rules"
	"os"
)

type Log struct {
	Initial rules.State
	Events  []rules.Event
}

func run() error {
	mode := flag.String("mode", "demo", "demo, coverage, or replay")
	path := flag.String("db", "data/cards/14.10.0/en/cards.json", "English snapshot")
	input := flag.String("input", "", "replay JSON file from demo stdout")
	flag.Parse()
	c, err := cards.Load(*path)
	if err != nil {
		return err
	}
	e, err := rules.New(c)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	switch *mode {
	case "coverage":
		return encoder.Encode(e.Coverage())
	case "replay":
		f, err := os.Open(*input)
		if err != nil {
			return err
		}
		defer f.Close()
		var log Log
		if err = json.NewDecoder(f).Decode(&log); err != nil {
			return err
		}
		state, err := e.Replay(log.Initial, log.Events)
		if err != nil {
			return err
		}
		return encoder.Encode(state)
	case "demo":
		s, err := e.Initial([2][]int{{152405, 202462, 113310}, {152405, 113301}}, [2][]int{}, 0)
		if err != nil {
			return err
		}
		log := Log{Initial: s}
		actions := []rules.Action{
			{Kind: "play", Card: "p0-0"}, {Kind: "play", Player: 1, Card: "p1-3"},
			{Kind: "play", Card: "p0-1", Target: "p0-0", Row: 1},
			{Kind: "play", Player: 1, Card: "p1-4", Target: "p0-1"},
			{Kind: "pass"}, {Kind: "pass", Player: 1},
			{Kind: "start_round"}, {Kind: "pass"}, {Kind: "pass", Player: 1},
		}
		for _, a := range actions {
			next, event, err := e.Apply(s, a)
			if err != nil {
				return err
			}
			log.Events = append(log.Events, event)
			s = next
		}
		if _, err = e.Replay(log.Initial, log.Events); err != nil {
			return err
		}
		return encoder.Encode(log)
	default:
		return fmt.Errorf("unknown mode %q", *mode)
	}
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
