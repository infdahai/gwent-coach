package cards

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
)

type Card struct {
	ID struct {
		Card int `json:"card"`
		Art  int `json:"art"`
	} `json:"id"`
	Name       string `json:"name"`
	Category   string `json:"category"`
	Ability    string `json:"ability"`
	Attributes struct {
		Set       string `json:"set"`
		Power     int    `json:"power"`
		Armor     int    `json:"armor"`
		Provision int    `json:"provision"`
		Faction   string `json:"faction"`
		Type      string `json:"type"`
	} `json:"attributes"`
}

type Catalog struct {
	Version  string       `json:"version"`
	Language string       `json:"language"`
	Cards    map[int]Card `json:"cards"`
}

func Load(path string) (*Catalog, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var source struct {
		Request struct {
			Status  int `json:"status"`
			Request struct {
				Version  string `json:"version"`
				Language string `json:"language"`
			} `json:"REQUEST"`
		} `json:"request"`
		Response map[string]Card `json:"response"`
	}
	if err := json.Unmarshal(b, &source); err != nil {
		return nil, err
	}
	if source.Request.Status != 200 || source.Request.Request.Version == "" || len(source.Response) == 0 {
		return nil, fmt.Errorf("invalid or empty card API snapshot")
	}
	c := &Catalog{Version: source.Request.Request.Version, Language: source.Request.Request.Language, Cards: make(map[int]Card, len(source.Response))}
	for _, card := range source.Response {
		if card.ID.Card <= 0 || card.Name == "" {
			return nil, fmt.Errorf("invalid card identity")
		}
		if _, exists := c.Cards[card.ID.Card]; exists {
			return nil, fmt.Errorf("duplicate card ID %d", card.ID.Card)
		}
		c.Cards[card.ID.Card] = card
	}
	return c, nil
}

func (c *Catalog) Find(query string) []Card {
	query = strings.ToLower(strings.TrimSpace(query))
	if id, err := strconv.Atoi(query); err == nil {
		if card, ok := c.Cards[id]; ok {
			return []Card{card}
		}
		return []Card{}
	}
	for _, card := range c.Cards {
		if strings.EqualFold(card.Name, query) {
			return []Card{card}
		}
	}
	result := []Card{}
	for _, card := range c.Cards {
		if query == "" || strings.Contains(strings.ToLower(card.Name), query) {
			result = append(result, card)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID.Card < result[j].ID.Card })
	return result
}

func (c Card) ArtURL() string {
	return fmt.Sprintf("https://gwent.one/image/gwent/assets/card/art/medium/%d.jpg", c.ID.Art)
}
