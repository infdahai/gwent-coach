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
	{132403, "Deploy: Boost self by 1 for each allied unit on this row.\n", "wolf_pack", 0},
	{202140, "Order: Boost an allied unit by 5.\n", "tactical_advantage", 5},
	{202218, "Deploy: Boost self by 1 for each card with a provision cost of 11 or higher in your starting deck.\n", "oxenfurt_scholar", 0},
	{142102, "Deploy: Boost all allied Elves by 1.\nWhenever you play an Elf, boost self by 1.\n", "isengrim_passive", 0},
	{202908, "Deploy: Damage an enemy unit by 2.\nWhen moved to the top of your deck during your turn, Summon self to a random allied row and damage a random enemy unit by 2.\n", "mage_assassin_deploy", 2},
	{200535, "Order (Melee): Damage an enemy unit by 1.\nCooldown: 2\nWhenever you play an Elf, decrease the Cooldown by 1.\n", "swordmaster_passive", 0},
	{202243, "Deploy: Damage an enemy unit by 2. If it has at least 6 power, damage by 4 instead.\n", "knight_errant", 2},
	// Only the observed no-enemy case is modeled; target and Bleeding effects
	// are rejected until the state model supports them.
	{202245, "Deploy: Damage an enemy unit by 2. If you have a Tactic in your hand, also give it Bleeding (2).\n", "keeper_empty_board", 0},
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
		if d.Kind == "wolf_pack" {
			kind, power = "Unit", 1
		}
		if d.Kind == "knight_errant" || d.Kind == "keeper_empty_board" {
			kind, power = "Unit", 3
		}
		if d.Kind == "oxenfurt_scholar" {
			kind, power = "Unit", 2
		}
		if d.Kind == "isengrim_passive" {
			kind, power = "Unit", 5
		}
		if d.Kind == "mage_assassin_deploy" {
			kind, power = "Unit", 1
		}
		if d.Kind == "swordmaster_passive" {
			kind, power = "Unit", 4
		}
		if d.Kind == "tactical_advantage" {
			kind = "Stratagem"
		}
		if !ok || card.Ability != d.Text || card.Attributes.Type != kind || card.Attributes.Power != power || card.Attributes.Armor != armor {
			return nil, fmt.Errorf("effect text mismatch for %d", d.ID)
		}
		e.effects[d.ID] = d
	}
	return e, nil
}

// Effect returns an audited native-v1 definition by value. Missing entries are unsupported.
func (e *Engine) Effect(id int) (Effect, bool) {
	effect, ok := e.effects[id]
	if effect.Kind == "keeper_empty_board" || effect.Kind == "mage_assassin_deploy" || effect.Kind == "swordmaster_passive" || effect.Kind == "isengrim_passive" {
		return Effect{}, false
	}
	return effect, ok
}

func (e *Engine) Coverage() []Coverage {
	result := make([]Coverage, 0, len(e.catalog.Cards))
	for _, c := range e.catalog.Find("") {
		reason := "unsupported: native ability not implemented"
		if effect, ok := e.effects[c.ID.Card]; ok && effect.Kind != "keeper_empty_board" && effect.Kind != "mage_assassin_deploy" && effect.Kind != "swordmaster_passive" && effect.Kind != "isengrim_passive" {
			reason = "native-v1: full printed ability; restricted ruleset"
			result = append(result, Coverage{c.ID.Card, c.Name, true, reason})
		} else {
			if ok {
				switch effect.Kind {
				case "keeper_empty_board":
					reason = "partial: only play with no enemy units is modeled"
				case "mage_assassin_deploy":
					reason = "partial: deployment only; deck movement trigger unsupported"
				case "swordmaster_passive", "isengrim_passive":
					reason = "partial: opponent board context only; its active turns unsupported"
				}
			}
			result = append(result, Coverage{c.ID.Card, c.Name, false, reason})
		}
	}
	return result
}
