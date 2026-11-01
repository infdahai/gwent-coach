package matchlog

import (
	"encoding/json"
	"fmt"
	"time"
)

// ValidateResultClaims checks the consistency of versioned claims, not the
// authenticity of the observations or the referenced frames. Legacy logs have
// no such contract and remain eligible only for quarantine.
func ValidateResultClaims(events []Event) error {
	var claimed *string
	for _, e := range events {
		if e.ResultContract == 0 && e.ResultPlayer == nil {
			continue
		}
		if e.EventType != "result_observed" || e.ResultContract != 1 || e.ResultPlayer == nil || (*e.ResultPlayer != 0 && *e.ResultPlayer != 1) || e.Result == nil || e.FramePath == nil || e.CapturedAt == nil || e.CalibrationID == "" {
			return fmt.Errorf("invalid result claim contract")
		}
		at, err := time.Parse(time.RFC3339Nano, *e.CapturedAt)
		if err != nil {
			return fmt.Errorf("invalid result capture time: %w", err)
		}
		if err := ValidateConfirmation(e.MatchID, *e.FramePath, at, e.ConfirmationFrames, time.Unix(0, 0)); err != nil {
			return fmt.Errorf("invalid result claim: %w", err)
		}
		data, err := json.Marshal(e.VisibleState)
		if err != nil {
			return err
		}
		var outcome struct {
			Finished *bool `json:"Finished"`
			Winner   *int  `json:"Winner"`
		}
		if err := json.Unmarshal(data, &outcome); err != nil || outcome.Finished == nil || !*outcome.Finished || outcome.Winner == nil || *outcome.Winner < -1 || *outcome.Winner > 1 {
			return fmt.Errorf("invalid result outcome claim")
		}
		result := "draw"
		if *outcome.Winner == *e.ResultPlayer {
			result = "win"
		} else if *outcome.Winner != -1 {
			result = "loss"
		}
		if *e.Result != result || (claimed != nil && *claimed != result) {
			return fmt.Errorf("conflicting result claims")
		}
		claimed = e.Result
	}
	if claimed != nil && (len(events) == 0 || events[len(events)-1].EventType != "match_completed" || events[len(events)-1].Result == nil || *events[len(events)-1].Result != *claimed) {
		return fmt.Errorf("terminal result differs from result claim")
	}
	return nil
}

// ValidateConfirmation binds two distinct ordered captures to this match and
// the final source. Both must follow actual transport completion.
func ValidateConfirmation(matchID, source string, at time.Time, frames []FrameEvidence, delivered time.Time) error {
	if matchID == "" || source == "" || at.IsZero() || delivered.IsZero() || len(frames) != 2 {
		return fmt.Errorf("confirmation requires bound match, source and two captures")
	}
	a, b := frames[0], frames[1]
	// Persisted timestamps do not retain Go's monotonic clock readings.
	a.CapturedAt, b.CapturedAt = a.CapturedAt.Round(0), b.CapturedAt.Round(0)
	at, delivered = at.Round(0), delivered.Round(0)
	if a.Source == "" || b.Source == "" || a.Source == b.Source || a.MatchID != matchID || b.MatchID != matchID || b.Source != source || !b.CapturedAt.Equal(at) || !a.CapturedAt.After(delivered) || !b.CapturedAt.After(a.CapturedAt) {
		return fmt.Errorf("confirmation captures are reused, foreign, stale or unordered")
	}
	return nil
}
