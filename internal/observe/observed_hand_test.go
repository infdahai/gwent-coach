package observe

import (
	"strings"
	"testing"
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

func TestLoadObservedHandValidatesAgainstLibrary(t *testing.T) {
	path, _, _ := observedConfigFixture(t)
	valid := &ObservedHandConfig{Database: "cards.db", Names: handTestNames}
	ids, err := loadObservedHand(path, valid)
	if err != nil {
		t.Fatal(err)
	}
	if !ids[200042] || !ids[132403] || len(ids) != 2 {
		t.Fatalf("ids=%v", ids)
	}
	for name, h := range map[string]*ObservedHandConfig{
		"empty-name":       {Database: "cards.db", Names: []ObservedCardName{{Name: "  ", CardID: 200042}, {Name: "狼群", CardID: 132403}}},
		"duplicate-name":   {Database: "cards.db", Names: []ObservedCardName{{Name: "玛哈坎勇士", CardID: 200042}, {Name: "玛 哈 坎 勇 士", CardID: 132403}}},
		"duplicate-id":     {Database: "cards.db", Names: []ObservedCardName{{Name: "玛哈坎勇士", CardID: 200042}, {Name: "狼群", CardID: 200042}}},
		"name-id-mismatch": {Database: "cards.db", Names: []ObservedCardName{{Name: "狼群", CardID: 200042}, {Name: "玛哈坎勇士", CardID: 132403}}},
		"not-whitelisted":  {Database: "cards.db", Names: []ObservedCardName{{Name: "牛堡学者", CardID: 202218}, {Name: "狼群", CardID: 132403}}},
		"missing-database": {Database: "missing.db", Names: handTestNames},
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
