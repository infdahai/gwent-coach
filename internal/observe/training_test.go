package observe

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTrainingPassOnlyGame(t *testing.T) {
	phases := []string{"training", "mulligan", "mulligan-confirm", "turn", "passed", "mulligan", "mulligan-confirm", "turn", "passed", "defeat"}
	index, frames := 0, 0
	var actions []string
	dir := filepath.Join(t.TempDir(), "trial")
	err := runTraining(Training{StateDir: dir, StopFile: "pause", Timeout: time.Second}, func() (navFrame, error) {
		frames++
		if index >= len(phases) {
			t.Fatal("unexpected capture")
		}
		return navFrame{Phase: phases[index], Captured: time.Now()}, nil
	}, func(_ navFrame, a trainingAction) error {
		actions = append(actions, a.kind)
		return nil
	}, func() error { return nil }, func() {
		if frames%2 == 0 && index < len(phases)-1 {
			index++
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(actions, ",") != "click,click,click,hold,click,click,hold" {
		t.Fatalf("actions: %v", actions)
	}
	data, err := os.ReadFile(filepath.Join(dir, "events.jsonl"))
	if err != nil || !strings.Contains(string(data), `"status":"result_confirmed"`) {
		t.Fatalf("ledger: %s %v", data, err)
	}
}

func TestTrainingRejectsUnconfirmedPass(t *testing.T) {
	for _, phase := range []string{"mulligan", "victory", "defeat", "draw"} {
		t.Run(phase, func(t *testing.T) {
			phases := []string{"training", "mulligan", "mulligan-confirm", "turn", phase}
			index, frames := 0, 0
			err := runTraining(Training{StateDir: filepath.Join(t.TempDir(), "trial"), StopFile: "pause", Timeout: time.Second}, func() (navFrame, error) {
				frames++
				return navFrame{Phase: phases[index], Captured: time.Now()}, nil
			}, func(navFrame, trainingAction) error { return nil }, func() error { return nil }, func() {
				if frames%2 == 0 && index < len(phases)-1 {
					index++
				}
			})
			if err == nil || !strings.Contains(err.Error(), "pass outcome unresolved") {
				t.Fatalf("expected unresolved pass, got %v", err)
			}
		})
	}
}

func TestTrainingReportsResultBeforeTurn(t *testing.T) {
	phases := []string{"mulligan-confirm", "defeat"}
	index, frames, sends := 0, 0, 0
	dir := filepath.Join(t.TempDir(), "trial")
	err := runTraining(Training{StateDir: dir, StopFile: "pause", Timeout: time.Second, ResumePhase: "confirm"}, func() (navFrame, error) {
		frames++
		return navFrame{Phase: phases[index], Captured: time.Now()}, nil
	}, func(navFrame, trainingAction) error { sends++; return nil }, func() error { return nil }, func() {
		if frames == 2 {
			index++
		}
	})
	if err == nil || !strings.Contains(err.Error(), "without confirmed turn action") || sends != 1 {
		t.Fatalf("err=%v sends=%d", err, sends)
	}
	data, err := os.ReadFile(filepath.Join(dir, "events.jsonl"))
	if err != nil || !strings.Contains(string(data), `"status":"result_unqualified"`) || strings.Contains(string(data), `"status":"result_confirmed"`) {
		t.Fatalf("ledger: %s %v", data, err)
	}
}

func TestTrainingReportsResultWhileAwaitingMulligan(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "trial")
	sends := 0
	err := runTraining(Training{StateDir: dir, StopFile: "pause", Timeout: time.Second, ResumePhase: "mulligan"}, func() (navFrame, error) {
		return navFrame{Phase: "defeat", Captured: time.Now()}, nil
	}, func(navFrame, trainingAction) error { sends++; return nil }, func() error { return nil }, func() {})
	if err == nil || !strings.Contains(err.Error(), "at mulligan") || sends != 0 {
		t.Fatalf("err=%v sends=%d", err, sends)
	}
}

func TestTrainingDoesNotRetryUncertainDelivery(t *testing.T) {
	sends := 0
	dir := filepath.Join(t.TempDir(), "trial")
	err := runTraining(Training{StateDir: dir, StopFile: "pause", Timeout: time.Second}, func() (navFrame, error) {
		return navFrame{Phase: "training", Captured: time.Now()}, nil
	}, func(navFrame, trainingAction) error { sends++; return errors.New("partial delivery") }, func() error { return nil }, func() {})
	if err == nil || sends != 1 {
		t.Fatalf("err=%v sends=%d", err, sends)
	}
	data, err := os.ReadFile(filepath.Join(dir, "events.jsonl"))
	if err != nil || !strings.Contains(string(data), "unresolved_do_not_replay") {
		t.Fatalf("ledger: %s %v", data, err)
	}
}
