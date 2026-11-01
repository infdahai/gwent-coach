package main

import (
	"encoding/json"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"os"
	"path/filepath"
	"time"

	"gwentcoachmvp/internal/cardindex"
	"gwentcoachmvp/internal/cardmatch"
	"gwentcoachmvp/internal/cards"
	"gwentcoachmvp/internal/rules"
)

// turnRehearsal is operator-supplied evidence, not a live state estimator.
type turnRehearsal struct {
	Database, Templates, Catalog string
	MatchID                      string
	Player                       int
	View                         rules.View
	HandOrder                    []string
	MaxDistance, MinGap          int
	Frames                       [2]struct {
		Image string
		Cards []struct {
			Instance string
			Art      cardmatch.ROI
			Click    image.Rectangle
		}
	}
	Rows [2]*image.Point
	Pass *image.Point
}

func rehearseTurn(path string, out io.Writer) error {
	var m turnRehearsal
	if err := cardindex.ReadJSON(path, &m); err != nil {
		return err
	}
	resolve := func(p string) string {
		if filepath.IsAbs(p) {
			return p
		}
		return filepath.Join(filepath.Dir(path), p)
	}
	version := cardindex.Version{Data: cardindex.DataVersion, Rules: cardindex.RulesVersion}
	db, err := cardindex.Open(resolve(m.Database), version)
	if err != nil {
		return err
	}
	var templates cardmatch.File
	if err := cardindex.ReadJSON(resolve(m.Templates), &templates); err != nil {
		return err
	}
	matcher, err := cardmatch.New(templates, db)
	if err != nil {
		return err
	}
	catalog, err := cards.Load(resolve(m.Catalog))
	if err != nil {
		return err
	}
	engine, err := rules.New(catalog)
	if err != nil {
		return err
	}
	service := rules.NewDecisionService(engine)
	defer service.Close()
	var frames [2]cardmatch.DecisionFrame
	var clicks [2]map[string]image.Rectangle
	for i, frame := range m.Frames {
		name := resolve(frame.Image)
		f, err := os.Open(name)
		if err != nil {
			return err
		}
		img, _, decodeErr := image.Decode(f)
		closeErr := f.Close()
		if decodeErr != nil {
			return decodeErr
		}
		if closeErr != nil {
			return closeErr
		}
		frames[i] = cardmatch.DecisionFrame{Source: name, Image: img}
		clicks[i] = make(map[string]image.Rectangle)
		for _, card := range frame.Cards {
			if card.Click.Empty() || !card.Click.In(img.Bounds()) {
				return fmt.Errorf("invalid click rectangle for %s", card.Instance)
			}
			frames[i].Cards = append(frames[i].Cards, cardmatch.ObservedCard{Instance: card.Instance, Art: card.Art})
			clicks[i][card.Instance] = card.Click
		}
	}
	if frames[0].Image.Bounds() != frames[1].Image.Bounds() {
		return fmt.Errorf("frame dimensions differ")
	}
	input := rules.DecisionInput{MatchID: m.MatchID, Phase: "turn", Player: m.Player, View: m.View, HandOrder: m.HandOrder, Deadline: time.Now().Add(5 * time.Second)}
	action, err := cardmatch.CandidateDecision(matcher, service, input, frames, cardmatch.Thresholds{MaxDistance: m.MaxDistance, MinGap: m.MinGap})
	if err != nil {
		return err
	}
	center := func(id string) (image.Point, error) {
		r, ok := clicks[0][id]
		if !ok || r != clicks[1][id] {
			return image.Point{}, fmt.Errorf("missing or inconsistent click rectangle for %s", id)
		}
		return image.Pt(r.Min.X+r.Dx()/2, r.Min.Y+r.Dy()/2), nil
	}
	result := struct {
		Action                  rules.Action `json:"action"`
		Card, Row, Target, Pass *image.Point `json:",omitempty"`
		Status                  string       `json:"status"`
	}{Action: action, Status: "offline_unvalidated_no_input"}
	if action.Kind == "pass" {
		if m.Pass == nil || !m.Pass.In(frames[0].Image.Bounds()) {
			return fmt.Errorf("pass point missing or outside frame")
		}
		result.Pass = m.Pass
	} else {
		p, err := center(action.Card)
		if err != nil {
			return err
		}
		result.Card = &p
		if action.Row < 0 || action.Row >= len(m.Rows) || m.Rows[action.Row] == nil || !m.Rows[action.Row].In(frames[0].Image.Bounds()) {
			return fmt.Errorf("row point missing or outside frame")
		}
		result.Row = m.Rows[action.Row]
		if action.Target != "" {
			p, err := center(action.Target)
			if err != nil {
				return err
			}
			result.Target = &p
		}
	}
	return json.NewEncoder(out).Encode(result)
}
