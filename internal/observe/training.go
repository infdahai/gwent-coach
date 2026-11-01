package observe

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Training runs one pass-only game from the training-versus-computer screen.
// The model must be calibrated for the local client and its current layout.
type Training struct {
	ModelPath, ModelSHA256, StopFile, StateDir string
	Timeout                                    time.Duration
	ResumePhase                                string
}

type trainingEvent struct {
	Time   time.Time `json:"time"`
	Status string    `json:"status"`
	Frame  navFrame  `json:"frame"`
	Action string    `json:"action,omitempty"`
	Error  string    `json:"error,omitempty"`
}

type trainingAction struct {
	kind string
	x, y int
}

// runTraining never retries delivered or uncertain input. It advances only on
// two matching fresh postconditions. Transient unknown frames are allowed while
// waiting, but never authorize input or confirm a transition.
func runTraining(t Training, observe func() (navFrame, error), send func(navFrame, trainingAction) error, pause func() error, wait func()) (runErr error) {
	if t.StateDir == "" || t.StopFile == "" || t.Timeout <= 0 {
		return fmt.Errorf("training requires a state directory, stop file and positive timeout")
	}
	if err := os.Mkdir(t.StateDir, 0700); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(t.StateDir, "events.jsonl"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	record := func(status string, frame navFrame, action string, cause error) error {
		e := trainingEvent{Time: time.Now().UTC(), Status: status, Frame: frame, Action: action}
		if cause != nil {
			e.Error = cause.Error()
		}
		if err := json.NewEncoder(f).Encode(e); err != nil {
			return err
		}
		return f.Sync()
	}
	defer func() {
		if runErr != nil {
			_ = record("stopped", navFrame{}, "", runErr)
		}
	}()
	deadline := time.Now().Add(t.Timeout)
	stable := ""
	count := 0
	stage := "start"
	if t.ResumePhase != "" {
		if t.ResumePhase != "mulligan" && t.ResumePhase != "confirm" {
			return fmt.Errorf("invalid reconciled resume phase")
		}
		stage = t.ResumePhase
	}
	round := 0
	for time.Now().Before(deadline) {
		if err := pause(); err != nil {
			return err
		}
		frame, err := observe()
		if err != nil {
			return err
		}
		if age := time.Since(frame.Captured); age < 0 || age > 2*time.Second {
			if err := record("stale_observation_discarded", frame, "", nil); err != nil {
				return err
			}
			stable, count = "", 0
			wait()
			continue
		}
		if err := record("observed", frame, "", nil); err != nil {
			return err
		}
		if frame.Phase == "unknown" || frame.Phase != stable {
			stable, count = frame.Phase, 1
		} else {
			count++
		}
		if count < 2 || stable == "unknown" {
			wait()
			continue
		}
		if (stage == "start" || stage == "mulligan" || stage == "confirm" || stage == "turn") && (stable == "victory" || stable == "defeat" || stable == "draw") {
			if err := record("result_unqualified", frame, stable, nil); err != nil {
				return err
			}
			return fmt.Errorf("result %s observed at %s without confirmed turn action", stable, stage)
		}
		var action trainingAction
		name, next := "", ""
		switch stage {
		case "start":
			if stable != "training" {
				return fmt.Errorf("expected training versus computer screen, got %q", stable)
			}
			name, next, action = "start_training", "mulligan", trainingAction{"click", 790, 555}
		case "mulligan":
			if stable == "mulligan" {
				round++
				if round > 3 {
					return fmt.Errorf("too many rounds")
				}
				name, next, action = "finish_mulligan", "confirm", trainingAction{"click", 870, 1020}
			}
		case "confirm":
			if stable == "mulligan-confirm" {
				name, next, action = "confirm_mulligan", "turn", trainingAction{"click", 892, 635}
			}
		case "turn":
			switch stable {
			case "turn":
				name, next, action = "pass", "after-pass", trainingAction{"hold", 1845, 540}
			}
		case "after-pass":
			switch stable {
			case "passed":
				stage = "round-end"
			case "mulligan", "victory", "defeat", "draw":
				return fmt.Errorf("pass outcome unresolved: reached %s without observing passed state", stable)
			}
		case "round-end":
			switch stable {
			case "mulligan":
				stage = "mulligan"
				continue
			case "victory", "defeat", "draw":
				return record("result_confirmed", frame, stable, nil)
			}
		}
		if name != "" {
			if err := pause(); err != nil {
				return err
			}
			if err := record("intent_outcome_pending", frame, name, nil); err != nil {
				return err
			}
			if !time.Now().Before(deadline) {
				return fmt.Errorf("training timeout at %s; inspect ledger before any retry", stage)
			}
			if err := send(frame, action); err != nil {
				_ = record("unresolved_do_not_replay", frame, name, err)
				return err
			}
			if err := record("delivered_unconfirmed", frame, name, nil); err != nil {
				return err
			}
			stage, stable, count = next, "", 0
		}
		wait()
	}
	return fmt.Errorf("training timeout at %s; inspect ledger before any retry", stage)
}
