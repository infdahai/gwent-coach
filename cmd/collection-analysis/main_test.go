package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAnalyzeCollectorRecords(t *testing.T) {
	input := `{"trust":"unverified_candidate_not_for_actions","reference":"C:\\runs\\frame1.json","captured_at":"2026-01-01T00:00:00Z","scalars":{"own_score":1},"unknown":["opponent_score"]}
{"trust":"unverified_candidate_not_for_actions","reference":"C:\\runs\\frame2.json","captured_at":"2026-01-01T00:00:01Z","scalars":{"own_score":1,"opponent_score":0},"unknown":[],"agreed":{"own_score":1}}
`
	got, err := analyze(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if got.Trust != candidateTrust || got.Frames != 2 || got.Scalars["own_score"].Candidates[1] != 2 || got.Scalars["own_score"].Agreed != 1 || got.Scalars["opponent_score"].Unknown != 1 || got.Scalars["opponent_score"].Candidates[0] != 1 {
		t.Fatalf("unexpected summary: %+v", got)
	}
	encoded, err := json.Marshal(got)
	if err != nil || !strings.Contains(string(encoded), candidateTrust) {
		t.Fatalf("missing trust marker: %s (%v)", encoded, err)
	}
}

func TestAnalyzeLabeledCandidates(t *testing.T) {
	input := `{"trust":"unverified_candidate_not_for_actions","reference":"frame1","captured_at":"2026-01-01T00:00:00Z","scalars":{"score":1,"other":2},"unknown":["hidden"]}
{"trust":"unverified_candidate_not_for_actions","reference":"frame2","captured_at":"2026-01-01T00:00:01Z","scalars":{"score":3},"unknown":[]}`
	labels := []annotation{{"frame1", map[string]int{"score": 1, "other": 3, "hidden": 0}}, {"frame2", map[string]int{"score": 2}}}
	got, err := analyzeLabeled(strings.NewReader(input), labels)
	if err != nil {
		t.Fatal(err)
	}
	if got.Trust != candidateTrust || *got.Evaluation["score"] != (evaluation{Correct: 1, Incorrect: 1}) || got.Evaluation["other"].Incorrect != 1 || got.Evaluation["hidden"].Unknown != 1 {
		t.Fatalf("unexpected evaluation: %+v", got.Evaluation)
	}
	for _, invalid := range [][]annotation{
		{}, {{"missing", map[string]int{"score": 1}}},
		{{"frame1", map[string]int{"absent": 1}}},
		{{"frame1", map[string]int{"score": 1}}, {"frame1", map[string]int{"score": 1}}},
	} {
		if _, err := analyzeLabeled(strings.NewReader(input), invalid); err == nil {
			t.Errorf("accepted invalid annotations: %+v", invalid)
		}
	}
	if _, err := analyzeLabeled(strings.NewReader(input+"\n"+strings.Split(input, "\n")[0]), labels); err == nil {
		t.Fatal("accepted duplicate annotated frame")
	}
}

func TestRunWithAnnotations(t *testing.T) {
	dir := t.TempDir()
	records := filepath.Join(dir, "recognition.jsonl")
	labels := filepath.Join(dir, "annotations.json")
	if err := os.WriteFile(records, []byte(`{"trust":"unverified_candidate_not_for_actions","reference":"frame","captured_at":"2026-01-01T00:00:00Z","scalars":{"score":0},"unknown":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(labels, []byte(`[{"reference":"frame","scalars":{"score":0}}]`), 0600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := run([]string{records, labels}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"correct": 1`) || !strings.Contains(out.String(), candidateTrust) {
		t.Fatal(out.String())
	}
	if err := os.WriteFile(labels, []byte(`[{"reference":"frame","scalars":{"score":null}}]`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{records, labels}, &out); err == nil {
		t.Fatal("accepted null label")
	}
}

func TestImportCollectionsIncremental(t *testing.T) {
	root := t.TempDir()
	source, dest := filepath.Join(root, "source"), filepath.Join(root, "dest")
	runDir := filepath.Join(source, "readonly-collection-1")
	for _, dir := range []string{runDir, dest} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	line := `{"trust":"unverified_candidate_not_for_actions","reference":"frame","captured_at":"2026-01-01T00:00:00Z","scalars":{"score":0},"unknown":[]}` + "\n"
	input := filepath.Join(runDir, "recognition.jsonl")
	write := func(data string) {
		t.Helper()
		if err := os.WriteFile(input, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(line + "{partial")
	var out bytes.Buffer
	if err := run([]string{"import", source, dest}, &out); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dest, "readonly-collection-1.jsonl")
	got, err := os.ReadFile(target)
	if err != nil || string(got) != line {
		t.Fatalf("partial import: %q %v", got, err)
	}
	write(line + line)
	if err := importCollections(source, dest, &out); err != nil {
		t.Fatal(err)
	}
	got, err = os.ReadFile(target)
	if err != nil || string(got) != line+line {
		t.Fatalf("incremental import: %q %v", got, err)
	}
	if err := importCollections(source, dest, &out); err != nil {
		t.Fatal(err)
	}
	write(line)
	if err := importCollections(source, dest, &out); err == nil {
		t.Fatal("accepted truncated source")
	}
	write(line + "{}\n")
	if err := importCollections(source, dest, &out); err == nil {
		t.Fatal("accepted invalid record")
	}
	got, _ = os.ReadFile(target)
	if string(got) != line+line {
		t.Fatal("overwrote imported data")
	}
	if err := importCollections(source, source, &out); err == nil {
		t.Fatal("accepted shared source and destination")
	}
}

func TestAnalyzeRejectsInvalidRecords(t *testing.T) {
	valid := `{"trust":"unverified_candidate_not_for_actions","reference":"frame","captured_at":"2026-01-01T00:00:00Z","scalars":{"score":1},"unknown":[]}`
	for _, input := range []string{
		"", "{}", "{", strings.Replace(valid, candidateTrust, "verified", 1),
		strings.Replace(valid, `"unknown":[]`, `"unknown":["score"]`, 1),
		strings.Replace(valid, `"unknown":[]`, `"unknown":[],"agreed":{"score":2}`, 1),
		valid + "\n{",
	} {
		if _, err := analyze(strings.NewReader(input)); err == nil {
			t.Errorf("accepted invalid input %q", input)
		}
	}
}
