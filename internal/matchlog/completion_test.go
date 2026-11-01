package matchlog

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestTrainingCompletionRecoversEachPublicationBoundary(t *testing.T) {
	for _, boundary := range []string{"sealed", "exported", "marked"} {
		t.Run(boundary, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "m")
			w, err := Create(dir, "m")
			if err != nil {
				t.Fatal(err)
			}
			for _, e := range protocolFixture() {
				if err = w.AppendDurable(e); err != nil {
					t.Fatal(err)
				}
			}
			if err = w.Seal(Event{EventType: "match_completed", Result: ptr("loss")}); err != nil {
				t.Fatal(err)
			}
			if _, err = os.Stat(filepath.Join(dir, "COMPLETE")); !os.IsNotExist(err) {
				t.Fatal("seal published COMPLETE before data")
			}
			events, err := Read(filepath.Join(dir, "events.jsonl"))
			if err != nil {
				t.Fatal(err)
			}
			if boundary != "sealed" {
				if err = ExportConfirmedTraining(filepath.Join(dir, "training.jsonl"), events); err != nil {
					t.Fatal(err)
				}
			}
			if boundary == "marked" {
				if err = CompleteTraining(dir, "m"); err != nil {
					t.Fatal(err)
				}
				if err = os.Remove(filepath.Join(dir, "training.jsonl")); err != nil {
					t.Fatal(err)
				}
			}
			if err = CompleteTraining(dir, "m"); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(filepath.Join(dir, "training.jsonl"))
			if err != nil || len(before) == 0 {
				t.Fatalf("dataset: %v", err)
			}
			if err = CompleteTraining(dir, "m"); err != nil {
				t.Fatal(err)
			}
			after, _ := os.ReadFile(filepath.Join(dir, "training.jsonl"))
			if !bytes.Equal(before, after) {
				t.Fatal("non-idempotent export")
			}
			if _, err = os.Stat(filepath.Join(dir, "COMPLETE")); err != nil {
				t.Fatal(err)
			}
		})
	}
}
