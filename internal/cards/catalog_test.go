package cards

import "testing"

func TestBilingualSnapshot(t *testing.T) {
	cn, err := Load("../../data/cards/14.10.0/cn/cards.json")
	if err != nil {
		t.Fatal(err)
	}
	en, err := Load("../../data/cards/14.10.0/en/cards.json")
	if err != nil {
		t.Fatal(err)
	}
	if cn.Version != en.Version || len(cn.Cards) != len(en.Cards) || len(cn.Cards) != 1381 {
		t.Fatal("snapshot versions or counts differ")
	}
	for id, card := range cn.Cards {
		other, ok := en.Cards[id]
		if !ok || card.ID.Art != other.ID.Art || card.Attributes != other.Attributes {
			t.Fatalf("bilingual identity/stat mismatch: %d", id)
		}
	}
	for _, tc := range []struct {
		query string
		id    int
	}{{"松鼠", 202600}, {"愤怒的暴民", 202667}, {"202248", 202248}} {
		got := cn.Find(tc.query)
		if len(got) != 1 || got[0].ID.Card != tc.id {
			t.Fatalf("lookup %q: %+v", tc.query, got)
		}
	}
	if len(cn.Find("not-a-real-card")) != 0 {
		t.Fatal("unknown names must remain unknown")
	}
}
