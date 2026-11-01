package main

import (
	"encoding/json"
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
