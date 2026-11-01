package observe

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"gwentcoachmvp/internal/matchlog"
)

type durableEvents interface{ AppendDurable(matchlog.Event) error }

// ActionJournal is owned by the serial loop/input controller. Observation
// producers never append to it. Sequence belongs to matchlog, not StateVersion.
type ActionJournal struct {
	writer           durableEvents
	pending          string
	domain           string
	matchID          string
	version          uint64
	step             uint64
	standalone       uint64
	delivered        time.Time
	complete         bool
	deliveryComplete bool
	autoEnded        bool
	original         Snapshot
	action           json.RawMessage
	steps            []RecordedInputStep
	result           *string
	confirmed        map[string]bool
}

func NewActionJournal(writer durableEvents) *ActionJournal {
	j := &ActionJournal{writer: writer, confirmed: map[string]bool{}}
	if identified, ok := writer.(interface{ MatchID() string }); ok {
		j.matchID = identified.MatchID()
	}
	return j
}

func (j *ActionJournal) Begin(id string, snapshot Snapshot, action any) error {
	if id == "" || snapshot.Version == 0 || j.pending != "" {
		return fmt.Errorf("action ID, version and no pending action required")
	}
	var observed struct{ MatchID string }
	if len(snapshot.State) > 0 {
		if err := json.Unmarshal(snapshot.State, &observed); err != nil {
			return err
		}
	}
	if observed.MatchID != "" {
		if j.matchID != "" && j.matchID != observed.MatchID {
			return fmt.Errorf("action snapshot belongs to another match")
		}
		j.matchID = observed.MatchID
	}
	if j.matchID == "" {
		return fmt.Errorf("bound match required for action journal")
	}
	domain := "gameplay"
	switch action.(type) {
	case Action, NavigationAction:
		domain = "navigation"
	}
	encoded, err := json.Marshal(action)
	if err != nil {
		return err
	}
	if err := j.writer.AppendDurable(matchlog.Event{EventType: "action_intent", ActionID: id, ActionDomain: domain, StateVersion: &snapshot.Version, Phase: &snapshot.Phase, FramePath: &snapshot.Source, VisibleState: json.RawMessage(snapshot.State), ActionIntent: action}); err != nil {
		return err
	}
	j.pending, j.version, j.step, j.delivered, j.complete = id, snapshot.Version, 0, time.Time{}, true
	j.domain = domain
	j.original = copySnapshot(snapshot)
	j.action = encoded
	j.deliveryComplete = false
	j.autoEnded = false
	j.steps = nil
	return nil
}

// ExecuteStep writes the durable intent before entering the platform adapter.
// The adapter calls onReceipt at delivery, before its settling wait/capture.
func (j *ActionJournal) ExecuteStep(action Action, execute func(Action) error) error {
	if j.pending != "" && j.deliveryComplete {
		return fmt.Errorf("action already delivered; confirm before another input")
	}
	if j.pending != "" && !j.complete {
		return fmt.Errorf("previous input step unresolved; do not replay")
	}
	id := j.pending
	var version *uint64
	if id == "" {
		j.standalone++
		id = fmt.Sprintf("input/%d", j.standalone)
	} else {
		v := j.version
		version = &v
	}
	j.step++
	step := j.step
	if err := j.writer.AppendDurable(matchlog.Event{EventType: "input_intent", ActionID: id, Step: &step, StateVersion: version, Phase: &action.ExpectedPhase, FramePath: &action.Reference, ActionIntent: action}); err != nil {
		return err
	}
	if id == j.pending {
		j.steps = append(j.steps, RecordedInputStep{Number: step, Input: action})
	}
	called := false
	var receiptErr error
	action.onReceipt = func(receipt InputReceipt) error {
		if called {
			return fmt.Errorf("duplicate input receipt")
		}
		called = true
		sent := int(receipt.EventsSent)
		var deliveredAt *string
		if !receipt.DeliveredAt.IsZero() {
			stamp := receipt.DeliveredAt.UTC().Format(time.RFC3339Nano)
			deliveredAt = &stamp
		}
		if id == j.pending {
			copyReceipt := receipt
			j.steps[len(j.steps)-1].Receipt = &copyReceipt
			if !(receipt.Outcome == "delivered" && receipt.EventsSent == 2) && !(action.Kind == "hover" && receipt.Outcome == "cursor_moved" && receipt.EventsSent == 0) {
				j.complete = false
			}
			if !receipt.DeliveredAt.IsZero() {
				j.delivered = receipt.DeliveredAt
			}
		}
		receiptErr = j.writer.AppendDurable(matchlog.Event{EventType: "input_receipt", ActionID: id, Step: &step, StateVersion: version, EventsSent: &sent, DeliveredAt: deliveredAt, DeliveryStatus: receipt.Outcome})
		if receiptErr != nil {
			j.complete = false
		}
		return receiptErr
	}
	err := errors.Join(execute(action), receiptErr)
	if !called {
		j.complete = false
		err = errors.Join(err, fmt.Errorf("input adapter returned without receipt; do not replay"))
	}
	if err != nil {
		j.complete = false
	}
	return err
}

func (j *ActionJournal) DeliveredAt(id string) (time.Time, error) {
	if j.pending != id || !j.complete || j.delivered.IsZero() {
		return time.Time{}, fmt.Errorf("action delivery incomplete; do not replay")
	}
	return j.delivered, nil
}

// MarkAutoEnd records the observed branch before the controller skips end-turn.
func (j *ActionJournal) MarkAutoEnd(id string, observation SemanticObservation) error {
	at, err := j.DeliveredAt(id)
	if err != nil {
		return err
	}
	if err = matchlog.ValidateConfirmation(j.matchID, observation.Source, observation.CapturedAt, observation.Frames, at); err != nil {
		return err
	}
	captured := observation.CapturedAt.UTC().Format(time.RFC3339Nano)
	if err := j.writer.AppendDurable(matchlog.Event{EventType: "action_auto_ended", ActionID: id, StateVersion: &j.version, FramePath: &observation.Source, CapturedAt: &captured, ConfirmationFrames: observation.Frames, VisibleState: observation.View}); err != nil {
		return err
	}
	j.autoEnded = true
	return nil
}

func (j *ActionJournal) MarkDelivered(id string) error {
	at, err := j.DeliveredAt(id)
	if err != nil {
		return err
	}
	stamp := at.UTC().Format(time.RFC3339Nano)
	if err = j.writer.AppendDurable(matchlog.Event{EventType: "action_delivered", ActionID: id, StateVersion: &j.version, DeliveredAt: &stamp}); err != nil {
		return err
	}
	j.deliveryComplete = true
	return nil
}

func (j *ActionJournal) Confirm(id string, snapshot Snapshot, observation SemanticObservation) error {
	if !j.deliveryComplete {
		return fmt.Errorf("action execution not complete")
	}
	if _, err := j.DeliveredAt(id); err != nil {
		return err
	}
	if snapshot.Version <= j.version || !observation.CapturedAt.After(j.delivered) {
		return fmt.Errorf("new post-delivery observation required")
	}
	if snapshot.Source != observation.Source || snapshot.Phase != observation.Phase || observation.MatchID != j.matchID {
		return fmt.Errorf("confirmation snapshot/source/match mismatch")
	}
	if err := matchlog.ValidateConfirmation(j.matchID, observation.Source, observation.CapturedAt, observation.Frames, j.delivered); err != nil {
		return err
	}
	captured := observation.CapturedAt.UTC().Format(time.RFC3339Nano)
	confirmed := "confirmed"
	if err := j.writer.AppendDurable(matchlog.Event{EventType: "action_confirmed", ActionID: id, ActionDomain: j.domain, StateVersion: &snapshot.Version, Phase: &observation.Phase, FramePath: &observation.Source, VisibleState: json.RawMessage(snapshot.State), CapturedAt: &captured, ConfirmationFrames: append([]matchlog.FrameEvidence(nil), observation.Frames...), Confirmation: &confirmed}); err != nil {
		return err
	}
	j.confirmed[id] = true
	j.pending = ""
	j.original = copySnapshot(snapshot)
	return nil
}

func (j *ActionJournal) Result(snapshot Snapshot, observation SemanticObservation) error {
	if j.pending != "" || !observation.Result {
		return fmt.Errorf("unresolved action or missing result observation")
	}
	result := "unknown"
	finished, winner := observation.View.Finished, observation.View.Winner
	var visible any = observation.View
	if observation.PartialResult != nil {
		finished, winner = observation.PartialResult.Finished, observation.PartialResult.Winner
		visible = observation.PartialResult
		if observation.Eligible || observation.CalibrationID == "" || snapshot.Source != observation.Source || snapshot.Phase != observation.Phase || observation.MatchID != j.matchID || observation.Player < 0 || observation.Player > 1 || !finished || winner < -1 || winner > 1 {
			return fmt.Errorf("invalid partial result observation")
		}
		if err := matchlog.ValidateConfirmation(j.matchID, observation.Source, observation.CapturedAt, observation.Frames, time.Unix(0, 0)); err != nil {
			return err
		}
	}
	if finished {
		switch winner {
		case observation.Player:
			result = "win"
		case 1 - observation.Player:
			result = "loss"
		case -1:
			result = "draw"
		}
	}
	captured := observation.CapturedAt.UTC().Format(time.RFC3339Nano)
	event := matchlog.Event{EventType: "result_observed", StateVersion: &snapshot.Version, Phase: &observation.Phase, FramePath: &observation.Source, VisibleState: visible, Result: &result, CapturedAt: &captured, ConfirmationFrames: append([]matchlog.FrameEvidence(nil), observation.Frames...), CalibrationID: observation.CalibrationID}
	if observation.PartialResult != nil {
		event.ResultContract, event.ResultPlayer = 1, &observation.Player
	}
	if err := j.writer.AppendDurable(event); err != nil {
		return err
	}
	j.result = &result
	j.original = copySnapshot(snapshot)
	return nil
}

func (j *ActionJournal) Unresolved(cause error) error {
	if j.pending == "" {
		return nil
	}
	text := cause.Error()
	return j.writer.AppendDurable(matchlog.Event{EventType: "action_unresolved", ActionID: j.pending, StateVersion: &j.version, Error: &text})
}
