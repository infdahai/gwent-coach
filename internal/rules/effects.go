// Package rules implements the deliberately restricted offline native-v1 ruleset.
package rules

import (
	"fmt"
	"gwentcoachmvp/internal/cards"
)

type Effect struct {
	ID         int
	Text, Kind string
	Amount     int
}

// These are complete abilities, pinned to the English 14.10.0 snapshot.
var definitions = []Effect{
	{152405, "No ability.\n", "unit", 0},
	{113301, "Damage a unit by 5.\n", "damage", 5},
	{113310, "Boost a unit by 6.\n", "boost", 6},
	{202465, "Damage a unit by 5. If it has Armor, damage by 7 instead.\n", "stunning", 5},
	{202411, "Damage an enemy unit by 4, ignoring its Armor.\n", "piercing", 4},
	{202462, "Deploy: Damage a unit by 2. If it's allied, also boost self by 4.\n", "plunderer", 2},
}

type Engine struct {
	catalog *cards.Catalog
	effects map[int]Effect
}

type Coverage struct {
	ID        int    `json:"id"`
	Name      string `json:"name"`
	Supported bool   `json:"supported"`
	Reason    string `json:"reason"`
}

func New(c *cards.Catalog) (*Engine, error) {
	if c == nil || c.Version != "14.10.0" || c.Language != "en" {
		return nil, fmt.Errorf("native effects require English 14.10.0 snapshot")
	}
	// Own a copy: caller mutations must not change replay behavior.
	copyCatalog := *c
	copyCatalog.Cards = make(map[int]cards.Card, len(c.Cards))
	for id, card := range c.Cards {
		copyCatalog.Cards[id] = card
	}
	e := &Engine{catalog: &copyCatalog, effects: map[int]Effect{}}
	for _, d := range definitions {
		card, ok := c.Cards[d.ID]
		kind, power, armor := "Special", 0, 0
		if d.Kind == "unit" {
			kind, power = "Unit", 5
		}
		if d.Kind == "plunderer" {
			kind, power, armor = "Unit", 3, 1
		}
		if !ok || card.Ability != d.Text || card.Attributes.Type != kind || card.Attributes.Power != power || card.Attributes.Armor != armor {
			return nil, fmt.Errorf("effect text mismatch for %d", d.ID)
		}
		e.effects[d.ID] = d
	}
	return e, nil
}

func (e *Engine) Coverage() []Coverage {
	result := make([]Coverage, 0, len(e.catalog.Cards))
	for _, c := range e.catalog.Find("") {
		_, ok := e.effects[c.ID.Card]
		reason := "unsupported: native ability not implemented"
		if ok {
			reason = "native-v1: full printed ability; restricted ruleset"
		}
		result = append(result, Coverage{c.ID.Card, c.Name, ok, reason})
	}
	return result
}
