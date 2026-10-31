package cardindex

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	bolt "go.etcd.io/bbolt"
)

var version = Version{DataVersion, RulesVersion}
var sources = []string{"../../data/cards/14.10.0/en/cards.json", "../../data/cards/14.10.0/cn/cards.json"}

func database(t testing.TB) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cards.db")
	if err := Import(path, version, sources); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestPersistence(t *testing.T) {
	path := database(t)
	for n := 0; n < 2; n++ {
		i, err := Open(path, version)
		if err != nil {
			t.Fatal(err)
		}
		if i.Len() != 1381 {
			t.Fatal(i.Len())
		}
		supportedCount := 0
		for id := range i.records {
			en, handler, supported, found := i.Lookup(id, "en")
			cn, _, _, localized := i.Lookup(id, "cn")
			if !found || !localized || en.ID != cn.ID {
				t.Fatalf("lost localized record %d", id)
			}
			if supported {
				supportedCount++
				if handler == "" {
					t.Fatal("missing handler")
				}
			} else if handler != "" {
				t.Fatal("unsupported handler")
			}
		}
		if supportedCount != 6 {
			t.Fatal(supportedCount)
		}
		card, handler, supported, ok := i.Lookup(113301, "en")
		if !ok || !supported || handler != "native-v1/damage" || card.Ability != "Damage a unit by 5.\n" {
			t.Fatal(card, handler, supported, ok)
		}
		if _, _, _, ok := i.Lookup(113301, "missing"); ok {
			t.Fatal("unknown language")
		}
		if _, _, _, ok := i.Lookup(-1, "en"); ok {
			t.Fatal("unknown ID")
		}
	}
	if err := Import(path, version, sources); err == nil {
		t.Fatal("overwrote existing version")
	}
	if _, err := Open(path, Version{"14.11.0", RulesVersion}); err == nil {
		t.Fatal("accepted data mismatch")
	}
	if _, err := Open(path, Version{DataVersion, "future"}); err == nil {
		t.Fatal("accepted rules mismatch")
	}
}

func TestRejectStoredMetadata(t *testing.T) {
	for _, change := range []string{"handler", "support", "version", "text"} {
		t.Run(change, func(t *testing.T) {
			path := database(t)
			db, err := bolt.Open(path, 0600, nil)
			if err != nil {
				t.Fatal(err)
			}
			err = db.Update(func(tx *bolt.Tx) error {
				root := tx.Bucket(bucket(version))
				if change == "version" {
					return root.Put([]byte("version"), []byte(`{"data_version":"other","rules_version":"native-v1"}`))
				}
				b := root.Bucket([]byte("cards"))
				var r Record
				if err := json.Unmarshal(b.Get([]byte("113301")), &r); err != nil {
					return err
				}
				switch change {
				case "handler":
					r.Handler = "native-v1/unknown"
				case "support":
					r.Supported = false
				case "text":
					r.Card.Ability = "Changed ability"
				}
				data, err := json.Marshal(r)
				if err != nil {
					return err
				}
				return b.Put([]byte("113301"), data)
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			if _, err := Open(path, version); err == nil {
				t.Fatal("invalid stored metadata accepted")
			}
		})
	}
}

func TestImportRejections(t *testing.T) {
	base, err := os.ReadFile(sources[0])
	if err != nil {
		t.Fatal(err)
	}
	var snapshot map[string]json.RawMessage
	if err := json.Unmarshal(base, &snapshot); err != nil {
		t.Fatal(err)
	}
	var rows map[string]json.RawMessage
	if err := json.Unmarshal(snapshot["response"], &rows); err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		rows["duplicate"] = r
		break
	}
	snapshot["response"], _ = json.Marshal(rows)
	duplicated, _ := json.Marshal(snapshot)
	for _, tc := range []struct {
		name    string
		data    []byte
		version Version
	}{
		{"duplicate identity", duplicated, version},
		{"duplicate JSON key", []byte(`{"response":{"1":{},"1":{}}}`), version},
		{"version mismatch", base, Version{"new-data", RulesVersion}},
		{"unknown rules", base, Version{DataVersion, "native-v2"}},
		{"trailing data", append(append([]byte(nil), base...), []byte(` {}`)...), version},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "source.json")
			if err := os.WriteFile(path, tc.data, 0600); err != nil {
				t.Fatal(err)
			}
			if err := Import(filepath.Join(dir, "cards.db"), tc.version, []string{path}); err == nil {
				t.Fatal("accepted invalid import")
			}
		})
	}
	if err := Import(filepath.Join(t.TempDir(), "cards.db"), version, []string{sources[1]}); err == nil {
		t.Fatal("accepted translation without English validation")
	}
	if err := Import(filepath.Join(t.TempDir(), "cards.db"), version, []string{sources[0], sources[0]}); err == nil {
		t.Fatal("duplicate language")
	}
}

func BenchmarkLookup(b *testing.B) {
	i, err := Open(database(b), version)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		if _, _, _, ok := i.Lookup(113301, "en"); !ok {
			b.Fatal("missing")
		}
	}
}
