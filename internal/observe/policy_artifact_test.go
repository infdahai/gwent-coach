package observe

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"gwentcoachmvp/internal/cardindex"
	"gwentcoachmvp/internal/strategy"
)

func TestMatchPolicyArtifactSelection(t *testing.T) {
	baseline, err := selectMatchPolicy("", "", nil)
	if err != nil || baseline.Version != "baseline-v1" || baseline.Policy != nil {
		t.Fatalf("baseline=%+v err=%v", baseline, err)
	}
	data, err := strategy.MarshalArtifact(strategy.Artifact{SchemaVersion: 1, PolicyVersion: "candidate-1", RulesVersion: cardindex.RulesVersion, CatalogVersion: cardindex.DataVersion, Payload: []byte(`{"kind":"action-ranker-v1","kind_weights":{"play":1}}`)})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "candidate.json")
	if err = os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	selected, err := selectMatchPolicy(path, hex.EncodeToString(sum[:]), nil)
	if err != nil || selected.Version != "candidate-1" || selected.Policy == nil {
		t.Fatalf("selected=%+v err=%v", selected, err)
	}
	rejected, err := selectMatchPolicy(path, hex.EncodeToString(make([]byte, 32)), nil)
	if err == nil || rejected.Version != "baseline-v1" || rejected.Policy != nil {
		t.Fatalf("rejected=%+v err=%v", rejected, err)
	}
}
