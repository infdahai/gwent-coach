// Package cardindex stores versioned card text and audited native support in bbolt.
package cardindex

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"time"

	"gwentcoachmvp/internal/cards"
	"gwentcoachmvp/internal/rules"

	bolt "go.etcd.io/bbolt"
)

const RulesVersion = "native-v1"
const DataVersion = "14.10.0"

type Version struct {
	Data  string `json:"data_version"`
	Rules string `json:"rules_version"`
}

func (v Version) validate() error {
	if v.Data != DataVersion || v.Rules != RulesVersion {
		return fmt.Errorf("unsupported version pair: %+v", v)
	}
	return nil
}

// Record keeps localized printed text separate from executable handler metadata.
// Handler identifies an audited rules definition; this package never executes text.
type Record struct {
	Card      cards.Card            `json:"card"`
	Localized map[string]cards.Card `json:"localized"`
	Supported bool                  `json:"supported"`
	Handler   string                `json:"handler,omitempty"`
}

type Index struct {
	version Version
	records map[int]Record
}

func (i *Index) Version() Version { return i.version }
func (i *Index) Len() int         { return len(i.records) }

// Lookup returns one localized card and its support metadata without exposing mutable maps.
func (i *Index) Lookup(id int, language string) (cards.Card, string, bool, bool) {
	r, ok := i.records[id]
	if !ok {
		return cards.Card{}, "", false, false
	}
	c, ok := r.Localized[language]
	if !ok {
		return cards.Card{}, "", false, false
	}
	return c, r.Handler, r.Supported, true
}

// ReadJSON rejects duplicate object keys, including duplicate source IDs that
// encoding/json would otherwise silently replace. It also rejects trailing data.
func ReadJSON(path string, target any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := uniqueJSON(json.NewDecoder(bytes.NewReader(b))); err != nil {
		return err
	}
	return json.Unmarshal(b, target)
}

func uniqueJSON(d *json.Decoder) error {
	var value func() error
	value = func() error {
		t, err := d.Token()
		if err != nil {
			return err
		}
		delim, ok := t.(json.Delim)
		if !ok {
			return nil
		}
		if delim == '{' {
			seen := map[string]bool{}
			for d.More() {
				key, err := d.Token()
				if err != nil {
					return err
				}
				s, ok := key.(string)
				if !ok {
					return fmt.Errorf("invalid object key")
				}
				if seen[s] {
					return fmt.Errorf("duplicate JSON key %q", s)
				}
				seen[s] = true
				if err := value(); err != nil {
					return err
				}
			}
		} else if delim == '[' {
			for d.More() {
				if err := value(); err != nil {
					return err
				}
			}
		} else {
			return fmt.Errorf("unexpected JSON delimiter")
		}
		_, err = d.Token()
		return err
	}
	if err := value(); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return fmt.Errorf("trailing JSON data: %v", err)
	}
	return nil
}

// Import reads every card in each supplied language snapshot. English is required
// to validate the native registry; translations must have identical IDs and stats.
// An existing version bucket is never overwritten.
func Import(path string, version Version, sources []string) error {
	if err := version.validate(); err != nil {
		return err
	}
	catalogs := map[string]*cards.Catalog{}
	for _, source := range sources {
		var raw struct {
			Request struct {
				Status  int `json:"status"`
				Request struct {
					Version  string `json:"version"`
					Language string `json:"language"`
				} `json:"REQUEST"`
			} `json:"request"`
			Response map[string]cards.Card `json:"response"`
		}
		if err := ReadJSON(source, &raw); err != nil {
			return err
		}
		lang := raw.Request.Request.Language
		if raw.Request.Status != 200 || raw.Request.Request.Version != version.Data || lang == "" || len(raw.Response) == 0 {
			return fmt.Errorf("invalid snapshot or version mismatch: %s", source)
		}
		if catalogs[lang] != nil {
			return fmt.Errorf("duplicate language %s", lang)
		}
		c := &cards.Catalog{Version: version.Data, Language: lang, Cards: map[int]cards.Card{}}
		for _, card := range raw.Response {
			if card.ID.Card <= 0 || card.Name == "" {
				return fmt.Errorf("invalid card identity")
			}
			if _, ok := c.Cards[card.ID.Card]; ok {
				return fmt.Errorf("duplicate card ID %d", card.ID.Card)
			}
			c.Cards[card.ID.Card] = card
		}
		catalogs[lang] = c
	}
	en := catalogs["en"]
	engine, err := rules.New(en)
	if err != nil {
		return err
	}
	records := map[int]Record{}
	for id, card := range en.Cards {
		r := Record{Card: card, Localized: map[string]cards.Card{}}
		if effect, ok := engine.Effect(id); ok {
			r.Supported, r.Handler = true, RulesVersion+"/"+effect.Kind
		}
		for lang, c := range catalogs {
			other, ok := c.Cards[id]
			if len(c.Cards) != len(en.Cards) || !ok || other.ID != card.ID || other.Attributes != card.Attributes {
				return fmt.Errorf("localized identity/stat mismatch: %s %d", lang, id)
			}
			r.Localized[lang] = other
		}
		records[id] = r
	}
	db, err := bolt.Open(path, 0600, &bolt.Options{Timeout: time.Second})
	if err != nil {
		return err
	}
	defer db.Close()
	return db.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucket(bucket(version))
		if err != nil {
			return err
		}
		meta, _ := json.Marshal(version)
		if err := b.Put([]byte("version"), meta); err != nil {
			return err
		}
		rows, err := b.CreateBucket([]byte("cards"))
		if err != nil {
			return err
		}
		for id, r := range records {
			data, err := json.Marshal(r)
			if err != nil {
				return err
			}
			if err := rows.Put([]byte(strconv.Itoa(id)), data); err != nil {
				return err
			}
		}
		return nil
	})
}

func bucket(v Version) []byte { return []byte(v.Data + "/" + v.Rules) }

// Open validates the persisted index against the current native registry once,
// then closes the file. Concurrent runtime lookups use an immutable in-memory map.
func Open(path string, version Version) (*Index, error) {
	if err := version.validate(); err != nil {
		return nil, err
	}
	db, err := bolt.Open(path, 0600, &bolt.Options{ReadOnly: true, Timeout: time.Second})
	if err != nil {
		return nil, err
	}
	defer db.Close()
	i := &Index{version: version, records: map[int]Record{}}
	err = db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucket(version))
		if b == nil {
			return fmt.Errorf("database version not found")
		}
		var stored Version
		if err := json.Unmarshal(b.Get([]byte("version")), &stored); err != nil {
			return err
		}
		if stored != version {
			return fmt.Errorf("database version mismatch")
		}
		rows := b.Bucket([]byte("cards"))
		if rows == nil {
			return fmt.Errorf("missing cards bucket")
		}
		return rows.ForEach(func(k, v []byte) error {
			var r Record
			if err := json.Unmarshal(v, &r); err != nil {
				return err
			}
			if string(k) != strconv.Itoa(r.Card.ID.Card) || r.Card.ID.Card <= 0 || r.Card.Name == "" {
				return fmt.Errorf("invalid stored card identity")
			}
			i.records[r.Card.ID.Card] = r
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	c := &cards.Catalog{Version: version.Data, Language: "en", Cards: map[int]cards.Card{}}
	for id, r := range i.records {
		c.Cards[id] = r.Card
	}
	e, err := rules.New(c)
	if err != nil {
		return nil, err
	}
	for id, r := range i.records {
		effect, supported := e.Effect(id)
		handler := ""
		if supported {
			handler = RulesVersion + "/" + effect.Kind
		}
		if r.Supported != supported || r.Handler != handler {
			return nil, fmt.Errorf("unknown or mismatched handler for %d", id)
		}
		if en, ok := r.Localized["en"]; !ok || en != r.Card {
			return nil, fmt.Errorf("missing or mismatched English record %d", id)
		}
		for _, other := range r.Localized {
			if other.ID != r.Card.ID || other.Attributes != r.Card.Attributes || other.Name == "" {
				return nil, fmt.Errorf("localized record mismatch %d", id)
			}
		}
	}
	return i, nil
}
