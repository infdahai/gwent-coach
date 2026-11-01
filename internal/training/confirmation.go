package training

import (
	"fmt"
	"time"
)

// ValidateConfirmation binds two distinct ordered captures to this match and
// the final source. Both must follow actual transport completion.
func ValidateConfirmation(matchID, source string, at time.Time, frames []FrameEvidence, delivered time.Time) error {
	if matchID == "" || source == "" || at.IsZero() || delivered.IsZero() || len(frames) != 2 {
		return fmt.Errorf("confirmation requires bound match, source and two captures")
	}
	a, b := frames[0], frames[1]
	if a.Source == "" || b.Source == "" || a.Source == b.Source || a.MatchID != matchID || b.MatchID != matchID || b.Source != source || !b.CapturedAt.Equal(at) || !a.CapturedAt.After(delivered) || !b.CapturedAt.After(a.CapturedAt) {
		return fmt.Errorf("confirmation captures are reused, foreign, stale or unordered")
	}
	return nil
}
