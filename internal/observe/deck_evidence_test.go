package observe

import (
	"encoding/json"
	"testing"

	"gwentcoachmvp/internal/cards"
)

func TestStartingHighProvisionsRequiresCompleteAuditedDeck(t *testing.T) {
	catalog, err := cards.Load("../../data/cards/14.10.0/en/cards.json")
	if err != nil {
		t.Fatal(err)
	}
	var audit auditDeck
	raw := `{"data_version":"14.10.0","rules_version":"native-v1","cards":[{"card_id":"202218","count":2},{"card_id":"132403","count":2},{"card_id":"202908","count":2},{"card_id":"202245","count":2},{"card_id":"202243","count":2},{"card_id":"202453","count":2},{"card_id":"202667","count":2},{"card_id":"202799","count":2},{"card_id":"202547","count":2},{"card_id":"202550","count":2},{"card_id":"162312","count":2},{"card_id":"202487","count":2},{"card_id":"112104","count":1}]}`
	if err := json.Unmarshal([]byte(raw), &audit); err != nil {
		t.Fatal(err)
	}
	got, err := startingHighProvisions(catalog, audit)
	if err != nil {
		t.Fatal(err)
	}
	if got != 1 {
		t.Fatalf("high=%d want=1", got)
	}
	audit.Cards[0].Count = 1
	if _, err := startingHighProvisions(catalog, audit); err == nil {
		t.Fatal("accepted incomplete deck")
	}
}
