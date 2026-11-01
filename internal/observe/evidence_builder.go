package observe

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"gwentcoachmvp/internal/stateestimate"
)

// EvidenceBuildGroup binds independently labelled samples to explicit values.
// Labels is a BuildPhases manifest relative to the builder manifest.
type EvidenceBuildGroup struct {
	Labels      string
	Values      map[string]json.RawMessage
	RequiredRGB []RGBRequirement
}

type EvidenceBuildManifest struct {
	StageModel, StageSHA256             string
	Round, Turn, Totals, Cards, Outcome *EvidenceBuildGroup
	Targets                             []EvidenceBuildGroup
	Scalars                             map[string]EvidenceBuildGroup
}

// BuildEvidence constructs a runtime calibration offline and exclusively creates out.
// The stage and label paths are relative to the manifest, and sample image paths
// are relative to their respective label manifests. No frames or facts are invented.
func BuildEvidence(manifestPath, out string) error {
	b, err := os.ReadFile(manifestPath)
	if err != nil {
		return err
	}
	var spec EvidenceBuildManifest
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err = dec.Decode(&spec); err != nil {
		return err
	}
	if dec.Decode(new(any)) != io.EOF {
		return fmt.Errorf("manifest must contain one JSON value")
	}
	base := filepath.Dir(manifestPath)
	stage, err := loadBoundPhases(filepath.Join(base, spec.StageModel), spec.StageSHA256)
	if err != nil {
		return fmt.Errorf("stage model: %w", err)
	}
	cal := EvidenceCalibration{Stage: *stage}
	if cal.Round, err = buildEvidenceGroup[stateestimate.RoundFacts](base, spec.Round); err != nil {
		return fmt.Errorf("round: %w", err)
	}
	if cal.Turn, err = buildEvidenceGroup[stateestimate.TurnFacts](base, spec.Turn); err != nil {
		return fmt.Errorf("turn: %w", err)
	}
	if cal.Totals, err = buildEvidenceGroup[stateestimate.TotalsFacts](base, spec.Totals); err != nil {
		return fmt.Errorf("totals: %w", err)
	}
	if cal.Cards, err = buildEvidenceGroup[stateestimate.CardsFacts](base, spec.Cards); err != nil {
		return fmt.Errorf("cards: %w", err)
	}
	if cal.Outcome, err = buildEvidenceGroup[stateestimate.OutcomeFacts](base, spec.Outcome); err != nil {
		return fmt.Errorf("outcome: %w", err)
	}
	for i := range spec.Targets {
		group, err := buildEvidenceGroup[stateestimate.Target](base, &spec.Targets[i])
		if err != nil {
			return fmt.Errorf("target %d: %w", i, err)
		}
		cal.Targets = append(cal.Targets, group)
	}
	if spec.Scalars != nil {
		cal.Scalars = make(map[string]*FactCalibration[int], len(spec.Scalars))
	}
	for name, group := range spec.Scalars {
		v, err := buildEvidenceGroup[int](base, &group)
		if err != nil {
			return fmt.Errorf("scalar %q: %w", name, err)
		}
		cal.Scalars[name] = v
	}
	if _, err = NewCalibratedEvidenceReader(cal); err != nil {
		return err
	}
	b, err = json.MarshalIndent(cal, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.OpenFile(out, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, writeErr := f.Write(b)
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}

func buildEvidenceGroup[T any](base string, group *EvidenceBuildGroup) (*FactCalibration[T], error) {
	if group == nil {
		return nil, nil
	}
	if group.Labels == "" {
		return nil, fmt.Errorf("label manifest required")
	}
	model, err := buildPhaseModel(filepath.Join(base, group.Labels))
	if err != nil {
		return nil, err
	}
	values := make(map[string]T, len(group.Values))
	for name, raw := range group.Values {
		var v T
		if err := requireExplicitValue(raw, reflect.TypeOf(v)); err != nil {
			return nil, fmt.Errorf("label %q: %w", name, err)
		}
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&v); err != nil {
			return nil, fmt.Errorf("label %q: %w", name, err)
		}
		values[name] = v
	}
	return &FactCalibration[T]{Model: *model, Values: values, RequiredRGB: group.RequiredRGB}, nil
}

// Require every field, array element and collection to be explicit; otherwise
// encoding/json would turn absent or null fields into apparently known zeroes.
func requireExplicitValue(raw json.RawMessage, typ reflect.Type) error {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return fmt.Errorf("null value for %s", typ)
	}
	switch typ.Kind() {
	case reflect.Struct:
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
			return fmt.Errorf("expected object for %s", typ)
		}
		for i := 0; i < typ.NumField(); i++ {
			field := typ.Field(i)
			if !field.IsExported() {
				continue
			}
			name := strings.Split(field.Tag.Get("json"), ",")[0]
			if name == "-" {
				continue
			}
			if name == "" {
				name = field.Name
			}
			value, ok := fields[name]
			if !ok {
				return fmt.Errorf("missing field %s", name)
			}
			if err := requireExplicitValue(value, field.Type); err != nil {
				return fmt.Errorf("%s: %w", name, err)
			}
		}
	case reflect.Array, reflect.Slice:
		var items []json.RawMessage
		if err := json.Unmarshal(raw, &items); err != nil || items == nil {
			return fmt.Errorf("expected array for %s", typ)
		}
		if typ.Kind() == reflect.Array && len(items) != typ.Len() {
			return fmt.Errorf("expected %d elements for %s", typ.Len(), typ)
		}
		for _, item := range items {
			if err := requireExplicitValue(item, typ.Elem()); err != nil {
				return err
			}
		}
	}
	return nil
}
