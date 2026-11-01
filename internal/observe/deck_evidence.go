package observe

import (
	"fmt"
	"strconv"

	"gwentcoachmvp/internal/cards"
)

// startingHighProvisions derives the scholar's public starting-deck fact from
// a complete audited deck and the pinned catalog, rather than a live hand.
func startingHighProvisions(catalog *cards.Catalog, audit auditDeck) (int, error) {
	if catalog == nil || catalog.Language != "en" || catalog.Version != audit.DataVersion || audit.RulesVersion != "native-v1" {
		return 0, fmt.Errorf("starting deck catalog/version mismatch")
	}
	seen := map[int]bool{}
	total, high := 0, 0
	for _, entry := range audit.Cards {
		id, err := strconv.Atoi(entry.CardID)
		card, ok := catalog.Cards[id]
		if err != nil || !ok || seen[id] || entry.Count < 1 || entry.Count > 2 {
			return 0, fmt.Errorf("invalid audited starting-deck card")
		}
		seen[id] = true
		total += entry.Count
		if card.Attributes.Provision >= 11 {
			high += entry.Count
		}
	}
	if total != 25 {
		return 0, fmt.Errorf("audited starting deck must contain 25 cards")
	}
	return high, nil
}
