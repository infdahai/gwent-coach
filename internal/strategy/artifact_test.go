package strategy

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestArtifactValidationAndMatchPinning(t *testing.T) {
	compat := Compatibility{RulesVersion: "rules-1", CatalogVersion: "catalog-1"}
	baseline := Artifact{SchemaVersion: 1, PolicyVersion: "baseline", RulesVersion: compat.RulesVersion, CatalogVersion: compat.CatalogVersion, Payload: []byte(`{"weight":1}`)}
	encode := func(a Artifact) []byte {
		t.Helper()
		b, err := MarshalArtifact(a)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	versions, err := NewPolicyVersions(encode(baseline), compat)
	if err != nil {
		t.Fatal(err)
	}
	pinned := versions.BeginMatch()
	candidate := baseline
	candidate.PolicyVersion = "candidate"
	candidate.Payload = []byte(`{"weight":2}`)
	if err := versions.Select(encode(candidate)); err != nil {
		t.Fatal(err)
	}
	if pinned.PolicyVersion != "baseline" || versions.BeginMatch().PolicyVersion != "candidate" {
		t.Fatal("match version was not pinned")
	}
	pinned.Payload[0] = 'x'
	current := versions.BeginMatch()
	current.Payload[0] = 'x'
	if bytes.Equal(current.Payload, versions.BeginMatch().Payload) {
		t.Fatal("match snapshot aliases selected payload")
	}
	bad := candidate
	bad.RulesVersion = "other"
	corrupt := encode(candidate)
	var envelope map[string]any
	if err := json.Unmarshal(corrupt, &envelope); err != nil {
		t.Fatal(err)
	}
	envelope["policy_version"] = "tampered"
	corrupt, _ = json.Marshal(envelope)
	wrongSchema := encode(candidate)
	wrongSchema = bytes.Replace(wrongSchema, []byte(`"schema_version":1`), []byte(`"schema_version":2`), 1)
	for name, data := range map[string][]byte{"incompatible": encode(bad), "corrupt": corrupt, "schema": wrongSchema, "invalid": []byte(`{`), "trailing": append(encode(candidate), []byte(` {}`)...)} {
		t.Run(name, func(t *testing.T) {
			if err := versions.Select(data); err == nil || !strings.Contains(err.Error(), "candidate rejected") {
				t.Fatalf("missing rejection reason: %v", err)
			}
			if versions.BeginMatch().PolicyVersion != "candidate" {
				t.Fatal("rejection changed active version")
			}
		})
	}
}

func TestVersionBinding(t *testing.T) {
	s := NewVersioned(nil, "v1")
	defer s.Close()
	o := observation()
	o.PolicyVersion = "v1"
	if err := s.Start(o); err != nil {
		t.Fatal(err)
	}
	changed := clone(o)
	changed.PolicyVersion = "v2"
	changed.Revision++
	if err := s.Start(changed); err == nil {
		t.Fatal("accepted different policy version")
	}
	if _, err := s.ResolveFallback(o, func(string) bool { return true }); err == nil {
		t.Fatal("old proposal survived version conflict")
	}
}
