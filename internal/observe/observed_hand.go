package observe

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"runtime"
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

// normalizeQuoted strips whitespace from s and returns the normalized text
// together with a boolean slice where true means the character at that index
// falls inside a quoted range. Word-internal apostrophes (e.g. "Renfri's") are
// literal, while single quotes that delimit a reference (e.g. 'Wolf Pack') are
// treated as quote delimiters. All ASCII letters are lowercased so that
// quoted-range tracking and name matching are case-insensitive.
func normalizeQuoted(s string) (string, []bool) {
	var t strings.Builder
	quoted := make([]bool, 0, len(s))

	// Self-pairing quote characters: each occurrence toggles per-type depth.
	selfPairing := map[rune]struct{}{
		'"': {},
	}

	// Paired quotes and brackets: opener -> closer. Single quotes are handled
	// separately because their word-internal use is not a delimiter.
	pairs := map[rune]rune{
		'“': '”', // “ ”
		'「': '」', // 「 」
		'『': '』', // 『 」
		'«': '»', // « »
	}
	// Reverse: closer -> opener
	openerOf := map[rune]rune{}
	for k, v := range pairs {
		openerOf[v] = k
	}

	depth := map[rune]int{}
	singleDepth := 0
	var bracketStack []rune
	runes := []rune(s)

	for i, r := range runes {
		if unicode.IsSpace(r) {
			continue
		}
		// Lowercase for case-insensitive tracking; letters only.
		norm := r
		if r >= 'A' && r <= 'Z' {
			norm = r + 32
		}
		t.WriteRune(norm)

		switch {
		case (r == '\'' || r == '’') && apostropheInsideWord(runes, i):
			// Possessive/contraction apostrophe: literal, not a delimiter.
		case r == '\'' || r == '’' || r == '‘':
			// Straight or typographic single quotes delimit a reference.
			singleDepth++
		default:
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
		}

		inAny := singleDepth%2 == 1
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

// apostropheInsideWord reports whether the quote at index i sits between two
// letters or digits, as in "Renfri's". Such an apostrophe is part of the card
// name and must not open or close a quoted reference.
func apostropheInsideWord(runes []rune, i int) bool {
	if i == 0 || i+1 >= len(runes) {
		return false
	}
	word := func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }
	return word(runes[i-1]) && word(runes[i+1])
}

// namedCard returns the configured card identified by the OCR text. Ability
// text quotes referenced cards, so names that fall inside a quoted range are
// ignored. When no name is found, or when more than one distinct configured
// card appears outside quotes, the identity is undetermined and namedCard
// returns 0 rather than guessing.
//
// The implementation tracks quote scope across the normalized (lowercased,
// whitespace-stripped) text. Both self-pairing quotes (") and paired brackets
// (「」『』«») are handled. A name that straddles a quote boundary is also
// rejected as undetermined. Matching is case-insensitive, consistent with
// EqualFold.
func namedCard(names []ObservedCardName, text string) int {
	t, quoted := normalizeQuoted(text)
	found := map[int]bool{}
	for _, n := range names {
		want := strings.ToLower(cardText(n.Name))
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

// hostAbsolutePath reports whether p is an absolute path for the current
// host. Native absolute paths always qualify. On non-Windows hosts a Windows
// target's drive-absolute ("D:\...") or UNC ("\\server\share\...") path also
// qualifies so a Mac-hosted preflight can inspect the config literally. On
// Windows only native filepath.IsAbs semantics apply, so root-relative paths
// such as "/tmp/STOP" or "\tmp\STOP" are not absolute.
func hostAbsolutePath(p string) bool {
	if p == "" {
		return false
	}
	if filepath.IsAbs(p) {
		return true
	}
	if runtime.GOOS == "windows" {
		return false
	}
	// UNC \\server\share[\...]: both server and share must be present.
	if len(p) >= 2 && p[0] == '\\' && p[1] == '\\' {
		rest := p[2:]
		sep := strings.IndexAny(rest, "\\/")
		if sep <= 0 {
			return false // no server component
		}
		share := rest[sep+1:]
		// A share is required; an immediately following separator means it is empty.
		return share != "" && share[0] != '\\' && share[0] != '/'
	}
	// Drive letter absolute, e.g. D:\path or D:/path (but not D:relative).
	if len(p) >= 3 && ((p[0] >= 'A' && p[0] <= 'Z') || (p[0] >= 'a' && p[0] <= 'z')) && p[1] == ':' && (p[2] == '\\' || p[2] == '/') {
		return true
	}
	return false
}

// loadObservedHand validates and loads the complete hand identity module in one
// place: calibrated geometry, mode-specific thresholds, the card database, and
// either tooltip names or art templates. It returns the set of known card IDs
// and leaves the resolved Database/Templates and matcher on h for the runtime.
//
// Names mode (len(h.Names) > 0) opens the database, validates each configured
// name, and sets no matcher; templates and art thresholds are unused.
// Art mode loads and validates the digest-pinned template index, builds the
// matcher, and requires explicit thresholds and guard regions.
func loadObservedHand(configPath string, h *ObservedHandConfig) (map[int]bool, error) {
	resolve := func(p string) string {
		if p == "" || filepath.IsAbs(p) {
			return p
		}
		return filepath.Join(filepath.Dir(configPath), p)
	}
	namesMode := len(h.Names) > 0
	bounds := image.Rect(0, 0, 1920, 1080)

	if len(h.Slots) < 1 || len(h.Slots) > 10 || len(h.Clicks) != len(h.Slots) {
		return nil, fmt.Errorf("1..10 calibrated hand slots and click points required")
	}
	// Art mode guards every slot with a post-hover region. Names mode identifies
	// by tooltip, so guard boxes are unused and may be omitted; if supplied they
	// must still be well formed.
	if namesMode {
		if len(h.GuardSlots) != 0 && len(h.GuardSlots) != len(h.Slots) {
			return nil, fmt.Errorf("guard slots must match hand slots when provided")
		}
	} else if len(h.GuardSlots) != len(h.Slots) {
		return nil, fmt.Errorf("guard slots required for art identification")
	}
	for i, roi := range h.Slots {
		regions := []cardmatch.ROI{roi}
		if len(h.GuardSlots) > 0 {
			regions = append(regions, h.GuardSlots[i])
		}
		for _, r := range regions {
			if r.Width < 9 || r.Height < 8 || !image.Rect(r.X, r.Y, r.X+r.Width, r.Y+r.Height).In(bounds) {
				return nil, fmt.Errorf("invalid hand art region")
			}
		}
		if !h.Clicks[i].In(image.Rect(roi.X, roi.Y, roi.X+roi.Width, roi.Y+roi.Height)) {
			return nil, fmt.Errorf("hand click must be inside its observed art region")
		}
	}
	// Art matching thresholds are meaningless without templates.
	if !namesMode {
		if err := (cardmatch.Thresholds{MaxDistance: h.MaxDistance, MinGap: h.MinGap}).Validate(); err != nil {
			return nil, err
		}
	}

	h.Database = resolve(h.Database)
	db, err := cardindex.Open(h.Database, cardindex.Version{Data: cardindex.DataVersion, Rules: cardindex.RulesVersion})
	if err != nil {
		return nil, err
	}
	if namesMode {
		return validateObservedNames(db, h)
	}

	h.Templates = resolve(h.Templates)
	templatesData, err := os.ReadFile(h.Templates)
	if err != nil {
		return nil, err
	}
	templateSum := sha256.Sum256(templatesData)
	if hex.EncodeToString(templateSum[:]) != h.TemplatesSHA256 {
		return nil, fmt.Errorf("hand template digest mismatch")
	}
	var templates cardmatch.File
	if err = json.Unmarshal(templatesData, &templates); err != nil {
		return nil, err
	}
	h.matcher, err = cardmatch.New(templates, db)
	if err != nil {
		return nil, err
	}
	knownIDs := map[int]bool{}
	for _, template := range templates.Templates {
		knownIDs[template.CardID] = true
	}
	if len(knownIDs) < 2 {
		return nil, fmt.Errorf("hand identity requires competing card templates")
	}
	return knownIDs, nil
}

// validateObservedNames checks tooltip identities against the card library and
// returns the verified IDs. Names must be unique, whitelisted, and match the
// card's localized rendering so a wrong name cannot stand in for a safe ID.
func validateObservedNames(db *cardindex.Index, h *ObservedHandConfig) (map[int]bool, error) {
	namedIDs := map[int]bool{}
	seenNames := map[string]bool{}

	if len(h.Names) < 2 {
		return nil, fmt.Errorf("hand identification requires competing card names")
	}

	for _, n := range h.Names {
		name := cardText(n.Name)
		if name == "" || seenNames[name] || namedIDs[n.CardID] || !safeObservedUnit(n.CardID) {
			return nil, fmt.Errorf("invalid or duplicate identified card name")
		}

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
