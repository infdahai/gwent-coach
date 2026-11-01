package observe

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"gwentcoachmvp/internal/matchlog"
	"gwentcoachmvp/internal/stateestimate"
)

// SemanticFromEvidence maps only a complete independently estimated view.
// Partial facts never become a zero-valued rules.View or action confirmation.
func SemanticFromEvidence(pair [2]stateestimate.Evidence, now time.Time, maxAge time.Duration) (SemanticObservation, error) {
	estimated, err := stateestimate.Estimate(pair, now, maxAge)
	if err != nil {
		return SemanticObservation{}, err
	}
	if estimated.View == nil {
		return SemanticObservation{}, fmt.Errorf("%w: missing %v", ErrUnstableVisual, estimated.Missing)
	}
	return SemanticObservation{Source: estimated.Frame.ID, MatchID: estimated.Frame.MatchID, Phase: estimated.Stage, Player: estimated.Frame.Player, CapturedAt: estimated.Frame.CapturedAt, View: *estimated.View, HandOrder: append([]string(nil), estimated.Facts.Cards.HandOrder...), Eligible: estimated.Eligible, Result: estimated.View.Finished, Frames: []matchlog.FrameEvidence{{Source: pair[0].Frame.ID, MatchID: pair[0].Frame.MatchID, CapturedAt: pair[0].Frame.CapturedAt}, {Source: pair[1].Frame.ID, MatchID: pair[1].Frame.MatchID, CapturedAt: pair[1].Frame.CapturedAt}}, CalibrationID: estimated.CalibrationID, StateKey: estimated.StateKey}, nil
}

func loadEvidenceReader(path, expectedHash string) (*CalibratedEvidenceReader, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(data)
	if len(expectedHash) != 64 || !strings.EqualFold(hex.EncodeToString(sum[:]), expectedHash) {
		return nil, fmt.Errorf("evidence calibration hash mismatch")
	}
	var calibration EvidenceCalibration
	if err = json.Unmarshal(data, &calibration); err != nil {
		return nil, err
	}
	return NewCalibratedEvidenceReader(calibration)
}
