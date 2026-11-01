package strategy

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"sync"
)

const ArtifactSchemaVersion = 1

// Artifact carries opaque policy data; interpretation belongs to the policy implementation.
// Payload is encoded as base64 in JSON. Digest covers all other fields.
type Artifact struct {
	SchemaVersion  int    `json:"schema_version"`
	PolicyVersion  string `json:"policy_version"`
	RulesVersion   string `json:"rules_version"`
	CatalogVersion string `json:"catalog_version"`
	Payload        []byte `json:"payload"`
	Digest         string `json:"digest"`
}

type Compatibility struct {
	RulesVersion   string
	CatalogVersion string
}

func artifactDigest(a Artifact) string {
	h := sha256.New()
	for _, field := range [][]byte{[]byte(strconv.Itoa(a.SchemaVersion)), []byte(a.PolicyVersion), []byte(a.RulesVersion), []byte(a.CatalogVersion), a.Payload} {
		var size [8]byte
		binary.BigEndian.PutUint64(size[:], uint64(len(field)))
		h.Write(size[:])
		h.Write(field)
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

// MarshalArtifact signs the content with an integrity digest, not an authenticity signature.
func MarshalArtifact(a Artifact) ([]byte, error) {
	if err := validateFields(a); err != nil {
		return nil, err
	}
	a.Digest = artifactDigest(a)
	return json.Marshal(a)
}

func validateFields(a Artifact) error {
	if a.SchemaVersion != ArtifactSchemaVersion {
		return fmt.Errorf("unsupported artifact schema %d", a.SchemaVersion)
	}
	if a.PolicyVersion == "" || a.RulesVersion == "" || a.CatalogVersion == "" || len(a.Payload) == 0 {
		return errors.New("artifact has empty required field")
	}
	return nil
}

// LoadArtifact validates the entire envelope and compatibility before returning it.
func LoadArtifact(data []byte, compatible Compatibility) (Artifact, error) {
	var a Artifact
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&a); err != nil {
		return Artifact{}, fmt.Errorf("decode artifact: %w", err)
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return Artifact{}, errors.New("artifact contains trailing data")
	}
	if err := validateFields(a); err != nil {
		return Artifact{}, err
	}
	if compatible.RulesVersion == "" || compatible.CatalogVersion == "" || a.RulesVersion != compatible.RulesVersion || a.CatalogVersion != compatible.CatalogVersion {
		return Artifact{}, fmt.Errorf("artifact incompatible: rules %q catalog %q", a.RulesVersion, a.CatalogVersion)
	}
	if a.Digest != artifactDigest(a) {
		return Artifact{}, errors.New("artifact digest mismatch")
	}
	return a, nil
}

func cloneArtifact(a Artifact) Artifact { a.Payload = bytes.Clone(a.Payload); return a }

// PolicyVersions holds the last accepted candidate. BeginMatch snapshots it;
// callers retain that snapshot for the whole match and construct their policy from it.
type PolicyVersions struct {
	mu         sync.Mutex
	compatible Compatibility
	selected   Artifact
}

// NewPolicyVersions validates the baseline using the same contract as candidates.
func NewPolicyVersions(baseline []byte, compatible Compatibility) (*PolicyVersions, error) {
	a, err := LoadArtifact(baseline, compatible)
	if err != nil {
		return nil, fmt.Errorf("baseline: %w", err)
	}
	return &PolicyVersions{compatible: compatible, selected: a}, nil
}

// Select accepts a candidate for future matches. A rejected candidate leaves the
// previously selected version active and returns the reason to the caller.
func (p *PolicyVersions) Select(data []byte) error {
	a, err := LoadArtifact(data, p.compatible)
	if err != nil {
		return fmt.Errorf("candidate rejected: %w", err)
	}
	p.mu.Lock()
	p.selected = a
	p.mu.Unlock()
	return nil
}

func (p *PolicyVersions) BeginMatch() Artifact {
	p.mu.Lock()
	defer p.mu.Unlock()
	return cloneArtifact(p.selected)
}
