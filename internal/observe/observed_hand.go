package observe

import (
	"fmt"
	"image"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"gwentcoachmvp/internal/cardindex"
	"gwentcoachmvp/internal/cardmatch"
)

// ObservedHandConfig describes calibrated hand-slot geometry and the card
// identity sources (art templates and/or hover tooltip names) the match loop
// uses to find a safe play.
type ObservedHandConfig struct {
	Database, Templates, TemplatesSHA256 string
	Slots, GuardSlots                    []cardmatch.ROI
	Clicks                               []image.Point
	MaxDistance, MinGap                  int
	// Names identifies hovered hand cards by the tooltip name the game renders.
	// It is the authoritative hand identity when present: art matching is not
	// used to choose a play in that mode.
	Names   []ObservedCardName `json:",omitempty"`
	matcher *cardmatch.Index
}

// ObservedCardName maps a rendered card name (as seen in the game tooltip) to
// its numeric card database ID.
type ObservedCardName struct {
	Name   string
	CardID int
}

func (h ObservedHandConfig) scanConfig(guard bool) HandScanConfig {
	slots := h.Slots
	if guard {
		slots = h.GuardSlots
	}
	return HandScanConfig{Width: 1920, Height: 1080, Slots: slots, MaxDistance: h.MaxDistance, MinGap: h.MinGap}
}

// These units have no deploy choice or target. Passive/order effects are left
// to the game; this policy neither activates orders nor predicts their effects.
func safeObservedUnit(id int) bool {
	switch id {
	case 201676, 142315, 203073, 200039, 203152, 142306,
		203256, 132403, 200042, // deploy effects resolve without a choice
		112202, 112203, 201559, 203089: // summon or passive deploy
		return true
	}
	return false
}

// cardText normalizes OCR output for name comparison. Windows OCR separates
// Han characters with spaces, so comparison ignores all whitespace.
func cardText(s string) string {
	var b strings.Builder
	for _, r := range s {
		if !unicode.IsSpace(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// namedCard returns the configured card identified by the OCR text. Ability
// text quotes referenced cards, so names that fall inside a quoted range are
// ignored. When no name is found, or when more than one distinct configured
// card appears outside quotes, the identity is undetermined and namedCard
// returns 0 rather than guessing.
//
// The implementation tracks quote scope across the normalized text: both
// self-pairing quotes (" ') and paired brackets (「」『』«») are handled. A name
// that straddles a quote boundary is also rejected as undetermined.
func namedCard(names []ObservedCardName, text string) int {
	t, quoted := normalizeQuoted(text)
	found := map[int]bool{}
	for _, n := range names {
		want := cardText(n.Name)
		if want == "" {
			continue
		}
		wantLen := utf8.RuneCountInString(want)
		for at := 0; at <= len(t); {
			idx := strings.Index(t[at:], want)
			if idx < 0 {
				break
			}
			pos := at + idx
			// Convert byte position to rune index for quoted-array lookup.
			runePos := utf8.RuneCountInString(t[:pos])
			runeEnd := runePos + wantLen - 1
			// A name that starts and ends outside quotes is the hovered title.
			// A match fully inside or crossing a quote boundary is not.
			if !quoted[runePos] && !quoted[runeEnd] {
				found[n.CardID] = true
			}
			at = pos + len(want)
		}
	}
	if len(found) != 1 {
		return 0
	}
	for id := range found {
		return id
	}
	return 0
}

// normalizeQuoted strips whitespace from s and returns the normalized text
// together with a boolean slice where true means the character at that index
// falls inside a quoted range. Self-pairing quotes (straight " and ')
// toggle per-type depth; paired brackets (「」, 『』, «») use a stack so each
// closer matches the most recent opener of the same kind.
func normalizeQuoted(s string) (string, []bool) {
	var t strings.Builder
	quoted := make([]bool, 0, len(s))

	// Self-pairing quote characters: each occurrence toggles per-type depth.
	selfPairing := map[rune]struct{}{
		'"':  {},
		'\'': {},
	}

	// Paired quotes and brackets: opener -> closer
	pairs := map[rune]rune{
		'“': '”', // “ ”
		'‘': '’', // ‘ ’
		'「': '」', // 「 」
		'『': '』', // 『 』
		'«': '»', // « »
	}
	// Reverse: closer -> opener
	openerOf := map[rune]rune{}
	for k, v := range pairs {
		openerOf[v] = k
	}

	depth := map[rune]int{}
	var bracketStack []rune

	for _, r := range s {
		if unicode.IsSpace(r) {
			continue
		}
		t.WriteRune(r)

		if _, ok := selfPairing[r]; ok {
			depth[r]++
		}

		if closer, ok := pairs[r]; ok {
			bracketStack = append(bracketStack, closer)
		} else if _, ok := openerOf[r]; ok {
			if len(bracketStack) > 0 && bracketStack[len(bracketStack)-1] == r {
				bracketStack = bracketStack[:len(bracketStack)-1]
			}
		}

		inAny := false
		for _, d := range depth {
			if d%2 == 1 {
				inAny = true
				break
			}
		}
		quoted = append(quoted, inAny || len(bracketStack) > 0)
	}

	return t.String(), quoted
}

// guardObservedCard verifies that the identified card in a specific hand slot
// has not changed since the last observed frame. It scans the slot's guard
// region and rejects any mismatch or ambiguity.
func guardObservedCard(img image.Image, hand ObservedHandConfig, slot, cardID int) error {
	if slot < 0 || slot >= len(hand.GuardSlots) {
		return fmt.Errorf("invalid guarded hand slot")
	}
	cfg := hand.scanConfig(true)
	cfg.Slots = cfg.Slots[slot : slot+1]
	matches, err := scanHandFrame(cfg, hand.matcher, img, false)
	if err != nil {
		return err
	}
	if matches[0].Status != "candidate" || matches[0].CardID != cardID || matches[0].RunnerDistance < 0 {
		return fmt.Errorf("guarded hand identity changed or unknown")
	}
	return nil
}

// loadObservedHand validates the Names entries in ObservedHandConfig against
// the card database and returns the set of verified, known card IDs. It skips
// template-based (art) validation; the caller is responsible for art mode setup
// separately. Returns error if any Name entry has an empty/non-matching card ID,
// duplicate names, or refers to an unsupported card. A single name entry is
// rejected (requires competition).
func loadObservedHand(configPath string, h *ObservedHandConfig) (map[int]bool, error) {
	namedIDs := map[int]bool{}
	seenNames := map[string]bool{}

	if len(h.Names) < 2 {
		return nil, fmt.Errorf("hand identification requires competing card names")
	}

	resolve := func(p string) string {
		if filepath.IsAbs(p) {
			return p
		}
		return filepath.Join(filepath.Dir(configPath), p)
	}

	dbPath := resolve(h.Database)
	db, err := cardindex.Open(dbPath, cardindex.Version{Data: cardindex.DataVersion, Rules: cardindex.RulesVersion})
	if err != nil {
		return nil, fmt.Errorf("opening card database %q: %w", dbPath, err)
	}

	for _, n := range h.Names {
		name := cardText(n.Name)
		if name == "" || seenNames[name] || namedIDs[n.CardID] || !safeObservedUnit(n.CardID) {
			return nil, fmt.Errorf("invalid or duplicate identified card name")
		}

		// The configured name must match this card's localized rendering in the
		// database, so a wrong name cannot stand in for a whitelisted card ID.
		matched := false
		for _, language := range []string{"cn", "en"} {
			if card, _, _, ok := db.Lookup(n.CardID, language); ok && strings.EqualFold(cardText(card.Name), name) {
				matched = true
				break
			}
		}
		if !matched {
			return nil, fmt.Errorf("card ID %d does not match configured name %q", n.CardID, n.Name)
		}

		seenNames[name], namedIDs[n.CardID] = true, true
	}

	return namedIDs, nil
}
