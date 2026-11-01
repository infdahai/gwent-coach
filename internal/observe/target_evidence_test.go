package observe

import (
	"image"
	"image/draw"
	"testing"
	"time"

	"gwentcoachmvp/internal/stateestimate"
)

func TestTargetEvidenceIndependentOfCompleteFacts(t *testing.T) {
	cal, frames := evidenceFixture()
	cal.Cards = nil
	target := stateestimate.Target{Kind: "hand-instance", ID: "own", LayoutID: "one-card", Bounds: image.Rect(10, 20, 40, 60)}
	cal.Targets = []*FactCalibration[stateestimate.Target]{evidenceFact([]image.Image{frames[0]}, []string{"own"}, []stateestimate.Target{target}, 128)}
	reader, err := NewCalibratedEvidenceReader(cal)
	if err != nil {
		t.Fatal(err)
	}
	pair, now := readEvidencePair(t, reader, frames[0])
	obs, err := stateestimate.Estimate(pair, now, time.Second)
	if err != nil || obs.View != nil || len(obs.Targets) != 1 || obs.Targets[0].Target != target || obs.Targets[0].Evidence.Frames[0].ID != "a" || !obs.FieldEvidence["turn"].Known || obs.FieldEvidence["cards"].Known {
		t.Fatalf("observation=%+v err=%v", obs, err)
	}
	pair[1].Targets[0].LayoutID = "two-cards"
	obs, err = stateestimate.Estimate(pair, now, time.Second)
	if err != nil || len(obs.Targets) != 0 {
		t.Fatalf("changed layout accepted: %+v %v", obs, err)
	}
	pair[1] = pair[0]
	if _, err = stateestimate.Estimate(pair, now, time.Second); err == nil {
		t.Fatal("duplicate capture accepted")
	}
	draw.Draw(frames[0], image.Rect(128, 0, 160, 8), image.Black, image.Point{}, draw.Src)
	pair, now = readEvidencePair(t, reader, frames[0])
	obs, err = stateestimate.Estimate(pair, now, time.Second)
	if err != nil || len(obs.Targets) != 0 {
		t.Fatalf("occluded target accepted: %+v %v", obs, err)
	}
}
