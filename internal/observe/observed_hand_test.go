package observe

import (
	"image"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"gwentcoachmvp/internal/cardmatch"
)

// Two whitelisted, non-person cards used by the name tests below.
var handTestNames = []ObservedCardName{
	{Name: "玛哈坎勇士", CardID: 200042},
	{Name: "狼群", CardID: 132403},
}

func TestLoadObservedMatchConfigOCROnlyNeedsNoTemplates(t *testing.T) {
	path, cfg, write := observedConfigFixture(t)
	cfg.Hand.Names = []ObservedCardName{
		{Name: "玛 哈 坎 勇 士", CardID: 200042},
		{Name: "狼群", CardID: 132403},
	}
	// Names mode must not require art assets or guard boxes.
	cfg.Hand.Templates, cfg.Hand.TemplatesSHA256 = "", ""
	cfg.Hand.GuardSlots = nil
	cfg.Plays[0].CardID = 200042
	write("match.json", cfg)

	loaded, err := LoadObservedMatchConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Hand.matcher != nil {
		t.Fatal("names mode built an art matcher from templates")
	}
	if len(loaded.Hand.Names) != 2 {
		t.Fatalf("names not retained: %+v", loaded.Hand.Names)
	}
}

func TestLoadObservedMatchConfigNamesModeDoesNotFallBackToArt(t *testing.T) {
	cases := map[string][]ObservedCardName{
		"single-name":    {{Name: "玛哈坎勇士", CardID: 200042}},
		"swapped-name":   {{Name: "狼群", CardID: 200042}, {Name: "玛哈坎勇士", CardID: 132403}},
		"duplicate-name": {{Name: "玛哈坎勇士", CardID: 200042}, {Name: "玛 哈 坎 勇 士", CardID: 132403}},
		"unsafe-card":    {{Name: "牛堡学者", CardID: 202218}, {Name: "狼群", CardID: 132403}},
	}
	for name, names := range cases {
		t.Run(name, func(t *testing.T) {
			path, cfg, write := observedConfigFixture(t)
			// Keep valid art assets configured; a failed names mode must not
			// silently fall back to them.
			cfg.Hand.Names = names
			cfg.Plays[0].CardID = names[0].CardID
			write("match.json", cfg)
			if _, err := LoadObservedMatchConfig(path); err == nil {
				t.Fatal("accepted invalid names mode by falling back to art")
			}
		})
	}
}

func TestLoadObservedMatchConfigArtModeUnchanged(t *testing.T) {
	t.Run("digest", func(t *testing.T) {
		path, cfg, write := observedConfigFixture(t)
		cfg.Hand.TemplatesSHA256 = strings.Repeat("0", 64)
		write("match.json", cfg)
		if _, err := LoadObservedMatchConfig(path); err == nil || !strings.Contains(err.Error(), "digest mismatch") {
			t.Fatalf("art mode accepted bad template digest: %v", err)
		}
	})
	t.Run("guard-slots-required", func(t *testing.T) {
		path, cfg, write := observedConfigFixture(t)
		cfg.Hand.GuardSlots = nil
		write("match.json", cfg)
		if _, err := LoadObservedMatchConfig(path); err == nil || !strings.Contains(err.Error(), "guard slots") {
			t.Fatalf("art mode accepted missing guard slots: %v", err)
		}
	})
}

func TestLoadObservedMatchConfigOCROnlyAcceptsZeroMinGap(t *testing.T) {
	path, cfg, write := observedConfigFixture(t)
	cfg.Hand.Names = []ObservedCardName{
		{Name: "玛 哈 坎 勇 士", CardID: 200042},
		{Name: "狼群", CardID: 132403},
	}
	cfg.Hand.Templates, cfg.Hand.TemplatesSHA256 = "", ""
	cfg.Hand.GuardSlots = nil
	cfg.Hand.MaxDistance, cfg.Hand.MinGap = 0, 0
	cfg.Plays[0].CardID = 200042
	write("match.json", cfg)
	if _, err := LoadObservedMatchConfig(path); err != nil {
		t.Fatalf("OCROnly with MinGap=0 should be accepted: %v", err)
	}
}

func TestLoadObservedHandValidatesAgainstLibrary(t *testing.T) {
	path, _, _ := observedConfigFixture(t)
	baseNames := &ObservedHandConfig{
		Database: "cards.db",
		Slots:    []cardmatch.ROI{{X: 450, Y: 900, Width: 100, Height: 100}, {X: 650, Y: 900, Width: 100, Height: 100}},
		Clicks:   []image.Point{{X: 500, Y: 950}, {X: 700, Y: 950}},
		Names:    handTestNames,
	}
	ids, err := loadObservedHand(path, baseNames)
	if err != nil {
		t.Fatal(err)
	}
	if !ids[200042] || !ids[132403] || len(ids) != 2 {
		t.Fatalf("ids=%v", ids)
	}
	for name, h := range map[string]*ObservedHandConfig{
		"empty-name":       {Database: "cards.db", Slots: baseNames.Slots, Clicks: baseNames.Clicks, Names: []ObservedCardName{{Name: "  ", CardID: 200042}, {Name: "狼群", CardID: 132403}}},
		"duplicate-name":   {Database: "cards.db", Slots: baseNames.Slots, Clicks: baseNames.Clicks, Names: []ObservedCardName{{Name: "玛哈坎勇士", CardID: 200042}, {Name: "玛 哈 坎 勇 士", CardID: 132403}}},
		"duplicate-id":     {Database: "cards.db", Slots: baseNames.Slots, Clicks: baseNames.Clicks, Names: []ObservedCardName{{Name: "玛哈坎勇士", CardID: 200042}, {Name: "狼群", CardID: 200042}}},
		"name-id-mismatch": {Database: "cards.db", Slots: baseNames.Slots, Clicks: baseNames.Clicks, Names: []ObservedCardName{{Name: "狼群", CardID: 200042}, {Name: "玛哈坎勇士", CardID: 132403}}},
		"not-whitelisted":  {Database: "cards.db", Slots: baseNames.Slots, Clicks: baseNames.Clicks, Names: []ObservedCardName{{Name: "牛堡学者", CardID: 202218}, {Name: "狼群", CardID: 132403}}},
		"missing-database": {Database: "missing.db", Slots: baseNames.Slots, Clicks: baseNames.Clicks, Names: handTestNames},
	} {
		if _, err := loadObservedHand(path, h); err == nil {
			t.Fatalf("%s: accepted invalid names", name)
		}
	}
}

func TestNamedCardRejectsAmbiguousOrQuotedOnlyText(t *testing.T) {
	cases := []struct {
		text string
		want int
	}{
		// Title outside quotes, referenced card inside quotes.
		{"卡 牌 大 师 狼 群 部 署 ： 从 牌 组 中 召 唤  “ 玛 哈 坎 勇 士 ”", 132403},
		// Both configured names appear unquoted: the title cannot be determined.
		{"卡牌大师狼群 玛哈坎勇士", 0},
		// Only a quoted reference is visible.
		{"“玛哈坎勇士”", 0},
		// Paired brackets mark the reference.
		{"卡牌大师狼群 部署：召唤「玛哈坎勇士」", 132403},
		// A balanced smart-quote range closes; a later title is still found.
		{"召唤“玛哈坎勇士”然后 狼群 出现", 132403},
		// Repeated occurrences of the same title are not ambiguous.
		{"狼群 狼群", 132403},
		// No configured card at all.
		{"卡牌大师9/10放弃跟牌", 0},
	}
	for _, c := range cases {
		if got := namedCard(handTestNames, c.text); got != c.want {
			t.Fatalf("namedCard(%q)=%d want %d", c.text, got, c.want)
		}
	}
}

func TestNamedCardRenfrisGang(t *testing.T) {
	// 203089 en="Renfri's Gang": the apostrophe must not be treated as a quote.
	names := []ObservedCardName{
		{Name: "Renfri's Gang", CardID: 203089},
		{Name: "狼群", CardID: 132403},
	}
	cases := []struct {
		text string
		want int
	}{
		{"Renfri's Gang", 203089},
		{"renfri's gang", 203089},
		{"RENFRI'S GANG", 203089},
		{"Renfri's Gang 狼群", 0}, // both unquoted => ambiguous
		{"“Renfri's Gang”", 0},  // fully quoted => rejected
	}
	for _, c := range cases {
		if got := namedCard(names, c.text); got != c.want {
			t.Fatalf("namedCard(%q)=%d want %d", c.text, got, c.want)
		}
	}
}

func TestNamedCardCaseInsensitive(t *testing.T) {
	names := []ObservedCardName{
		{Name: "玛哈坎勇士", CardID: 200042},
		// "狼群" is all lowercase; test that mixed-case config names also match.
		{Name: "Wolf Pack", CardID: 132403},
	}
	cases := []struct {
		text string
		want int
	}{
		{"wolf pack", 132403},
		{"WOLF PACK", 132403},
		{"Wolf Pack", 132403},
	}
	for _, c := range cases {
		if got := namedCard(names, c.text); got != c.want {
			t.Fatalf("namedCard(%q)=%d want %d", c.text, got, c.want)
		}
	}
}

func TestNamedCardQuotedReferences(t *testing.T) {
	names := []ObservedCardName{
		{Name: "Wolf Pack", CardID: 132403},
		{Name: "Renfri's Gang", CardID: 203089},
	}
	cases := []struct {
		text string
		want int
	}{
		{"Create 'Wolf Pack'", 0},                     // quoted reference only
		{"'Renfri's Gang'", 0},                        // quoted name with apostrophe
		{"Wolf Pack  Create 'Renfri's Gang'", 132403}, // hovered title plus quoted reference
		{"Renfri's Gang", 203089},                     // the title itself stays identifiable
		{"Create 'Wolf Pack' Renfri's Gang", 203089},  // unquoted title wins over quoted text
		{"'Wolf Pack' 'Renfri's Gang'", 0},            // multiple quoted references
		{"Wolf Pack Create 'Renfri's Gang'", 132403},  // quoted reference remains excluded
	}
	for _, c := range cases {
		if got := namedCard(names, c.text); got != c.want {
			t.Fatalf("namedCard(%q)=%d want %d", c.text, got, c.want)
		}
	}
}

func TestHostAbsolutePath(t *testing.T) {
	cases := map[string]bool{
		`D:\gwent\STOP`:       true,
		`D:/gwent/STOP`:       true,
		`c:\state`:            true,
		`\\server\share\STOP`: true,
		`\\server\share`:      true,
		`\\server`:            false, // missing share
		`D:relative`:          false, // drive-relative, not drive-absolute
		`relative/STOP`:       false,
		`STOP`:                false,
		``:                    false,
	}
	if runtime.GOOS == "windows" {
		// Native Windows semantics only: root-relative slash paths are not
		// absolute and must not be accepted as STOP/StateDir locations.
		cases["/tmp/STOP"] = false
		cases[`\tmp\STOP`] = false
		// Native Go reports bare UNC roots as absolute too; follow it.
		cases[`\\server`] = filepath.IsAbs(`\\server`)
		cases[`\\server\share`] = filepath.IsAbs(`\\server\share`)
	} else {
		cases["/tmp/STOP"] = true
	}
	for p, want := range cases {
		if got := hostAbsolutePath(p); got != want {
			t.Fatalf("hostAbsolutePath(%q)=%v want %v", p, got, want)
		}
	}
}

func TestHostAbsolutePathWindowsSemantics(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows filepath.IsAbs semantics")
	}
	for _, p := range []string{"/tmp/STOP", `\tmp\STOP`, `D:\gwent\STOP`, `\\server\share\STOP`, "relative"} {
		if got, want := hostAbsolutePath(p), filepath.IsAbs(p); got != want {
			t.Fatalf("hostAbsolutePath(%q)=%v want native %v", p, got, want)
		}
	}
}

func TestLoadObservedMatchConfigWindowsPathsPreserved(t *testing.T) {
	path, cfg, write := observedConfigFixture(t)
	cfg.StopFile = `D:\gwent\STOP`
	cfg.StateDir = `D:\gwent\state`
	write("match.json", cfg)

	loaded, err := LoadObservedMatchConfig(path)
	if err != nil {
		t.Fatalf("Mac loader rejected a Windows target's absolute StopFile/StateDir: %v", err)
	}
	// Foreign absolute paths must stay literal rather than being joined onto a
	// local directory.
	if loaded.StopFile != `D:\gwent\STOP` {
		t.Fatalf("StopFile rewritten: %q", loaded.StopFile)
	}
	if loaded.StateDir != `D:\gwent\state` {
		t.Fatalf("StateDir rewritten: %q", loaded.StateDir)
	}
}

func TestLoadObservedMatchConfigRejectsRelativeStop(t *testing.T) {
	path, cfg, write := observedConfigFixture(t)
	cfg.StopFile = "STOP"
	write("match.json", cfg)
	if _, err := LoadObservedMatchConfig(path); err == nil {
		t.Fatal("accepted relative StopFile")
	}
}

func TestLoadObservedMatchConfigWindowsRejectsRootRelativeStop(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows native path semantics")
	}
	// A drive-less root-relative path is not an absolute Windows path, so it
	// must not be accepted for STOP (or a StateDir on another drive).
	for _, stop := range []string{"/tmp/STOP", `\tmp\STOP`} {
		path, cfg, write := observedConfigFixture(t)
		cfg.StopFile = stop
		write("match.json", cfg)
		if _, err := LoadObservedMatchConfig(path); err == nil {
			t.Fatalf("accepted non-native absolute StopFile %q", stop)
		}
	}
}
