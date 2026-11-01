package observe

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"gwentcoachmvp/internal/matchlog"
	"gwentcoachmvp/internal/uiphase"
)

type NavigationAction struct {
	Input Action
	To    string
}
type SessionTransition struct {
	From, To string
	Input    Action
}

// TrainingSession owns serial durable writes across entry, a LocalLoop match,
// result exit and the next entry. It contains no recognizer, policy or input API.
type TrainingSession struct {
	ID, LogRoot            string
	StartMatch, Matches    int
	ResumeFirst            bool
	Bootstrap, Entry, Exit []SessionTransition
	TransitionBudget       time.Duration
	ObserveStage           func(context.Context, string) (uiphase.Event, error)
	ExecuteInput           func(context.Context, Action) error
	RunMatch               func(context.Context, string, *ActionJournal) (SemanticObservation, error)
}

func safeSessionID(id string) bool {
	return regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`).MatchString(id)
}

func (s TrainingSession) Run(ctx context.Context) error {
	if !safeSessionID(s.ID) || s.LogRoot == "" || s.StartMatch < 1 || s.Matches < 1 || s.TransitionBudget <= 0 || s.ObserveStage == nil || s.ExecuteInput == nil || s.RunMatch == nil {
		return fmt.Errorf("session configuration and callbacks required")
	}
	for n := s.StartMatch; n < s.StartMatch+s.Matches; n++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		id := fmt.Sprintf("%s-%06d", s.ID, n)
		dir := filepath.Join(s.LogRoot, id)
		var writer *matchlog.Writer
		var events []matchlog.Event
		var err error
		if n == s.StartMatch && s.ResumeFirst {
			closed, e := matchlog.Read(filepath.Join(dir, "events.jsonl"))
			if e != nil {
				return e
			}
			if len(closed) > 0 && closed[len(closed)-1].EventType == "match_completed" {
				if e = matchlog.CompleteTraining(dir, id); e != nil {
					return e
				}
				continue
			}
			writer, events, err = matchlog.Resume(dir, id)
		} else {
			writer, err = matchlog.Create(dir, id)
		}
		if err != nil {
			return err
		}
		err = s.runOne(ctx, id, writer, events)
		closeErr := writer.Abort()
		if err != nil || closeErr != nil {
			return errors.Join(err, closeErr)
		}
		if err = matchlog.CompleteTraining(dir, id); err != nil {
			return err
		}
	}
	return nil
}

func (s TrainingSession) runOne(ctx context.Context, id string, writer *matchlog.Writer, events []matchlog.Event) (runErr error) {
	journal, err := RestoreActionJournal(writer, events)
	if err != nil {
		return err
	}
	if len(events) == 0 {
		if err := writer.AppendDurable(matchlog.Event{EventType: "session_started", SessionID: s.ID}); err != nil {
			return err
		}
	}
	defer func() {
		if runErr != nil {
			runErr = errors.Join(runErr, journal.Unresolved(runErr))
		}
	}()
	confirmed := map[string]bool{}
	var savedResult *string
	for _, e := range events {
		if e.EventType == "action_confirmed" {
			confirmed[e.ActionID] = true
		}
		if e.EventType == "result_observed" {
			savedResult = e.Result
		}
	}
	if id == fmt.Sprintf("%s-%06d", s.ID, s.StartMatch) {
		for i, t := range s.Bootstrap {
			actionID := fmt.Sprintf("%s/bootstrap/%d", id, i)
			if !confirmed[actionID] {
				if err = s.navigate(ctx, id, actionID, t, journal); err != nil {
					return err
				}
			}
		}
	}
	for i, t := range s.Entry {
		actionID := fmt.Sprintf("%s/entry/%d", id, i)
		if !confirmed[actionID] {
			if err = s.navigate(ctx, id, actionID, t, journal); err != nil {
				return err
			}
		}
	}
	rounds := map[int]bool{}
	for _, e := range events {
		prefix := id + "/round/"
		if e.EventType == "action_intent" && e.ActionDomain == "navigation" && strings.HasPrefix(e.ActionID, prefix) {
			parts := strings.Split(strings.TrimPrefix(e.ActionID, prefix), "/")
			if len(parts) != 2 {
				return fmt.Errorf("invalid persisted round navigation ID")
			}
			r, e := strconv.Atoi(parts[0])
			if e != nil || r < 2 || r > 3 {
				return fmt.Errorf("invalid persisted round")
			}
			rounds[r] = true
		}
	}
	var orderedRounds []int
	for r := range rounds {
		orderedRounds = append(orderedRounds, r)
	}
	sort.Ints(orderedRounds)
	for _, r := range orderedRounds {
		if err = s.FinishMulligan(ctx, id, r, journal); err != nil {
			return err
		}
	}
	var result string
	if savedResult != nil && (journal.Pending() == nil || journal.Pending().Domain == "navigation") {
		result = *savedResult
	} else {
		observation, e := s.RunMatch(ctx, id, journal)
		if e != nil {
			return e
		}
		if !observation.Result || observation.MatchID != id || journal.Pending() != nil || journal.result == nil {
			return fmt.Errorf("match returned without confirmed terminal observation")
		}
		result = "unknown"
		if observation.View.Finished {
			switch observation.View.Winner {
			case observation.Player:
				result = "win"
			case 1 - observation.Player:
				result = "loss"
			case -1:
				result = "draw"
			}
		}
		// LocalLoop owns result_observed; callbacks must run that loop with this journal.
	}
	if result == "unknown" {
		return fmt.Errorf("terminal outcome unresolved; match remains incomplete")
	}
	for i, t := range s.Exit {
		actionID := fmt.Sprintf("%s/exit/%d", id, i)
		if !confirmed[actionID] {
			if err = s.navigate(ctx, id, actionID, t, journal); err != nil {
				return err
			}
		}
	}
	return writer.Seal(matchlog.Event{EventType: "match_completed", Result: &result})
}

func stageSnapshot(j *ActionJournal, id string, event uiphase.Event) Snapshot {
	state, _ := json.Marshal(struct{ MatchID, Stage string }{id, event.Phase})
	version := j.original.Version
	if version == 0 || j.original.Phase != event.Phase || !bytes.Equal(j.original.State, state) {
		version++
	}
	return Snapshot{Version: version, Source: event.Evidence.Frame.ID, Phase: event.Phase, State: state}
}

func (s TrainingSession) waitStage(ctx context.Context, id, want string, after time.Time, j *ActionJournal) (uiphase.Event, error) {
	for {
		if err := ctx.Err(); err != nil {
			return uiphase.Event{}, err
		}
		e, err := s.ObserveStage(ctx, id)
		if err != nil {
			return uiphase.Event{}, err
		}
		phase := e.Phase
		source := e.Evidence.Frame.ID
		if err = j.writer.AppendDurable(matchlog.Event{EventType: "ui_stage_observed", Phase: &phase, FramePath: &source, VisibleState: e}); err != nil {
			return uiphase.Event{}, err
		}
		fresh := time.Since(e.Sources[0].CapturedAt) >= 0 && time.Since(e.Sources[0].CapturedAt) <= 2*time.Second && !e.Evidence.Frame.CapturedAt.After(time.Now())
		bound := e.Sources[0].StreamID == id && e.Sources[1].StreamID == id && e.Sources[1].ID == e.Evidence.Frame.ID && e.Sources[1].CapturedAt.Equal(e.Evidence.Frame.CapturedAt) && e.Sources[1].CapturedAt.After(e.Sources[0].CapturedAt)
		if bound && fresh && e.Confirmed && e.Phase == want && e.Evidence.Frame.StreamID == id && e.Sources[0].ID != "" && e.Sources[0].ID != e.Sources[1].ID && (after.IsZero() || e.Sources[0].CapturedAt.After(after)) {
			return e, nil
		}
		select {
		case <-ctx.Done():
			return uiphase.Event{}, ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// FinishMulligan reuses the calibrated entry suffix for a later round. Each
// navigation input is recorded and confirmed by the same session controller.
func (s TrainingSession) FinishMulligan(ctx context.Context, id string, round int, j *ActionJournal) error {
	started := false
	for i, t := range s.Entry {
		if t.From == "mulligan" {
			started = true
		}
		if started {
			actionID := fmt.Sprintf("%s/round/%d/%d", id, round, i)
			if j.confirmed[actionID] {
				continue
			}
			if err := s.navigate(ctx, id, actionID, t, j); err != nil {
				return err
			}
		}
	}
	if !started {
		return fmt.Errorf("session profile has no mulligan path")
	}
	return nil
}

func (s TrainingSession) navigate(ctx context.Context, id, actionID string, t SessionTransition, j *ActionJournal) error {
	ctx, cancel := context.WithTimeout(ctx, s.TransitionBudget)
	defer cancel()
	pending := j.Pending()
	plan := NavigationAction{Input: t.Input, To: t.To}
	if pending != nil {
		if pending.ID != actionID || pending.Domain != "navigation" || !pending.DeliveryComplete {
			return fmt.Errorf("original navigation step unresolved; do not reselect")
		}
		if err := json.Unmarshal(pending.Action, &plan); err != nil {
			return err
		}
	} else {
		before, err := s.waitStage(ctx, id, t.From, time.Time{}, j)
		if err != nil {
			return err
		}
		plan.Input.Reference = before.Evidence.Frame.ID
		plan.Input.deadline, _ = ctx.Deadline()
		plan.Input.ExpectedPhase = t.From
		if err = j.Begin(actionID, stageSnapshot(j, id, before), plan); err != nil {
			return err
		}
		if err = j.ExecuteStep(plan.Input, func(a Action) error { return s.ExecuteInput(ctx, a) }); err != nil {
			return err
		}
		if err = j.MarkDelivered(actionID); err != nil {
			return err
		}
	}
	at, err := j.DeliveredAt(actionID)
	if err != nil {
		return err
	}
	after, err := s.waitStage(ctx, id, plan.To, at, j)
	if err != nil {
		return err
	}
	snapshot := stageSnapshot(j, id, after)
	return j.Confirm(actionID, snapshot, SemanticObservation{Source: snapshot.Source, MatchID: id, Phase: after.Phase, CapturedAt: after.Evidence.Frame.CapturedAt, Frames: []matchlog.FrameEvidence{{Source: after.Sources[0].ID, MatchID: after.Sources[0].StreamID, CapturedAt: after.Sources[0].CapturedAt}, {Source: after.Sources[1].ID, MatchID: after.Sources[1].StreamID, CapturedAt: after.Sources[1].CapturedAt}}})
}
