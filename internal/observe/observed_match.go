package observe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"gwentcoachmvp/internal/cardmatch"
)

type observedMatchFrame struct {
	navFrame
	Image string             `json:"image,omitempty"`
	Plays map[string]string  `json:"plays,omitempty"`
	Hand  []cardmatch.Result `json:"hand,omitempty"`
}

type observedMatchInput struct {
	Name       string `json:"name"`
	Action     Action `json:"action"`
	PlayID     string `json:"play_id,omitempty"`
	Slot       int    `json:"slot,omitempty"`
	Identified bool   `json:"identified_by_name,omitempty"`
}

type observedMatchEvent struct {
	Time    time.Time            `json:"time"`
	Status  string               `json:"status"`
	Round   int                  `json:"round"`
	Plays   int                  `json:"confirmed_plays"`
	Frames  []observedMatchFrame `json:"frames,omitempty"`
	Input   *observedMatchInput  `json:"input,omitempty"`
	Receipt *InputReceipt        `json:"receipt,omitempty"`
	Error   string               `json:"error,omitempty"`
}

type observedMatchHost struct {
	Observe  func(context.Context) (observedMatchFrame, error)
	Send     func(context.Context, observedMatchFrame, observedMatchInput, func(InputReceipt) error) error
	Identify func(context.Context, observedMatchFrame, int) (int, observedMatchFrame, error)
	Pause    func() error
	Wait     func(context.Context) error
	Progress func(observedMatchEvent) error
}

// runObservedMatch never recovers by replaying an input. A new process also
// requires reconciliation of the shared durable interlock, even after success.
func runObservedMatch(ctx context.Context, cfg ObservedMatchConfig, host observedMatchHost, output io.Writer) (runErr error) {
	ctx, cancel := context.WithTimeout(ctx, time.Duration(cfg.TimeoutMS)*time.Millisecond)
	defer cancel()
	round, plays := 0, 0
	record := func(status string, frames []observedMatchFrame, input *observedMatchInput, receipt *InputReceipt, cause error) error {
		e := observedMatchEvent{Time: time.Now().UTC(), Status: status, Round: round, Plays: plays, Frames: frames, Input: input, Receipt: receipt}
		if cause != nil {
			e.Error = cause.Error()
		}
		if err := json.NewEncoder(output).Encode(e); err != nil {
			return err
		}
		if host.Progress != nil {
			return host.Progress(e)
		}
		return nil
	}
	defer func() {
		if runErr != nil {
			runErr = errors.Join(runErr, record("stopped_do_not_replay", nil, nil, nil, runErr))
		}
	}()
	check := func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		return host.Pause()
	}
	var last observedMatchFrame
	var notBefore time.Time
	// Each await starts a new pair. Both frames must follow delivery, have
	// independent identities/timestamps, and still be fresh at confirmation.
	await := func(playID string, expected, waiting []string) ([2]observedMatchFrame, error) {
		waitCtx, cancelWait := context.WithTimeout(ctx, time.Duration(cfg.TransitionTimeoutMS)*time.Millisecond)
		defer cancelWait()
		deadline, _ := waitCtx.Deadline()
		var previous observedMatchFrame
		var unknownSince time.Time
		contains := func(xs []string, s string) bool {
			for _, x := range xs {
				if x == s {
					return true
				}
			}
			return false
		}
		for time.Now().Before(deadline) {
			if err := check(); err != nil {
				return [2]observedMatchFrame{}, err
			}
			frame, err := host.Observe(waitCtx)
			if err != nil {
				return [2]observedMatchFrame{}, err
			}
			if err = waitCtx.Err(); err != nil {
				return [2]observedMatchFrame{}, err
			}
			if err = check(); err != nil {
				return [2]observedMatchFrame{}, err
			}
			if frame.Reference == "" || frame.Width != 1920 || frame.Height != 1080 {
				return [2]observedMatchFrame{}, fmt.Errorf("invalid native observation")
			}
			if frame.Reference == last.Reference || (!last.Captured.IsZero() && !frame.Captured.After(last.Captured)) {
				return [2]observedMatchFrame{}, fmt.Errorf("observation identity or timestamp replay")
			}
			last = frame
			if age := time.Since(frame.Captured); age < 0 || age > 2*time.Second || !frame.Captured.After(notBefore) {
				previous = observedMatchFrame{}
				if err = record("stale_observation_discarded", []observedMatchFrame{frame}, nil, nil, nil); err != nil {
					return [2]observedMatchFrame{}, err
				}
				if err = host.Wait(waitCtx); err != nil {
					return [2]observedMatchFrame{}, err
				}
				continue
			}
			if err = record("observed", []observedMatchFrame{frame}, nil, nil, nil); err != nil {
				return [2]observedMatchFrame{}, err
			}
			phase := frame.Phase
			prior := previous.Phase
			if playID != "" {
				phase, prior = frame.Plays[playID], previous.Plays[playID]
			}
			if phase == "" || phase == "unknown" {
				if unknownSince.IsZero() {
					unknownSince = time.Now()
				}
				if time.Since(unknownSince) >= time.Duration(cfg.UnknownTimeoutMS)*time.Millisecond {
					return [2]observedMatchFrame{}, fmt.Errorf("unknown screen while awaiting %v", expected)
				}
				previous = observedMatchFrame{}
			} else {
				unknownSince = time.Time{}
				if contains(expected, phase) {
					if previous.Reference != "" && prior == phase && time.Since(previous.Captured) <= 2*time.Second {
						return [2]observedMatchFrame{previous, frame}, nil
					}
					previous = frame
				} else if contains(waiting, phase) {
					previous = observedMatchFrame{}
				} else {
					return [2]observedMatchFrame{}, fmt.Errorf("unexpected actionable phase %q while awaiting %v", phase, expected)
				}
			}
			if err = host.Wait(waitCtx); err != nil {
				return [2]observedMatchFrame{}, err
			}
		}
		return [2]observedMatchFrame{}, fmt.Errorf("transition timeout awaiting %v; do not replay", expected)
	}
	send := func(pair [2]observedMatchFrame, input observedMatchInput) error {
		if err := check(); err != nil {
			return err
		}
		for _, f := range pair {
			if age := time.Since(f.Captured); age < 0 || age > 2*time.Second {
				return fmt.Errorf("input evidence expired")
			}
		}
		if err := record("input_intent", pair[:], &input, nil, nil); err != nil {
			return err
		}
		received := false
		var receipt InputReceipt
		err := host.Send(ctx, pair[1], input, func(r InputReceipt) error {
			if received {
				return fmt.Errorf("duplicate input receipt")
			}
			received, receipt = true, r
			return record("input_receipt", nil, &input, &r, nil)
		})
		if err != nil {
			return err
		}
		if !received || receipt.Outcome != "delivered" || receipt.EventsSent != 2 || receipt.DeliveredAt.IsZero() || receipt.DeliveredAt.Before(pair[1].Captured) || receipt.DeliveredAt.After(time.Now()) {
			return fmt.Errorf("input delivery unresolved; do not replay")
		}
		notBefore = receipt.DeliveredAt
		return record("delivered_unconfirmed", nil, &input, &receipt, nil)
	}
	click := func(name string, x, y int, playID string) observedMatchInput {
		return observedMatchInput{Name: name, Action: Action{Kind: "click", X: x, Y: y}, PlayID: playID}
	}
	confirm := func(name string, pair [2]observedMatchFrame) error {
		return record(name+"_confirmed", pair[:], nil, nil, nil)
	}
	pair, err := await("", []string{"training"}, nil)
	if err != nil {
		return err
	}
	if err = send(pair, click("start_training", cfg.Start.X, cfg.Start.Y, "")); err != nil {
		return err
	}
	pair, err = await("", []string{"mulligan"}, []string{"training", "transition"})
	if err != nil {
		return err
	}
	if err = confirm("start_training", pair); err != nil {
		return err
	}
	used := map[string]bool{}
	for {
		round++
		if round > 3 {
			return fmt.Errorf("too many observed rounds")
		}
		if err = confirm("round_start", pair); err != nil {
			return err
		}
		if err = send(pair, click("finish_mulligan", cfg.FinishMulligan.X, cfg.FinishMulligan.Y, "")); err != nil {
			return err
		}
		pair, err = await("", []string{"mulligan-confirm"}, []string{"mulligan", "transition"})
		if err != nil {
			return err
		}
		if err = confirm("finish_mulligan", pair); err != nil {
			return err
		}
		if err = send(pair, click("confirm_mulligan", cfg.ConfirmMulligan.X, cfg.ConfirmMulligan.Y, "")); err != nil {
			return err
		}
		pair, err = await("", []string{"turn"}, []string{"mulligan-confirm", "opponent", "transition"})
		if err != nil {
			return err
		}
		if err = confirm("confirm_mulligan", pair); err != nil {
			return err
		}
		for pair[1].Phase == "turn" {
			var play *ObservedPlay
			slot := -1
			identified := false
			if len(cfg.Hand.Names) > 0 {
				// Named tooltips are the authoritative hand identity. Hover each
				// slot until a configured card that can still be played is seen.
				want := map[int]bool{}
				for i := range cfg.Plays {
					p := &cfg.Plays[i]
					if !used[p.ID] && pair[0].Plays[p.ID] == "ready" && pair[1].Plays[p.ID] == "ready" {
						want[p.CardID] = true
					}
				}
				if len(want) > 0 {
					if host.Identify == nil {
						return fmt.Errorf("hand name identification required but unavailable")
					}
					for j := 0; j < len(cfg.Hand.Clicks) && pair[1].Phase == "turn"; j++ {
						id, hovered, err := host.Identify(ctx, pair[1], j)
						if err != nil {
							return err
						}
						pair[1] = hovered
						if id == 0 || !want[id] {
							continue
						}
						for i := range cfg.Plays {
							p := &cfg.Plays[i]
							if !used[p.ID] && p.CardID == id {
								play, slot, identified = p, j, true
								break
							}
						}
						if play != nil {
							break
						}
					}
				}
				if play != nil {
					// The hovered card stays under the cursor: refresh the pair so
					// the click is authorized by a frame of the settled hover.
					var refreshErr error
					pair, refreshErr = await("", []string{"turn"}, nil)
					if refreshErr != nil {
						return refreshErr
					}
					if pair[1].Plays[play.ID] != "ready" {
						return fmt.Errorf("identified hand slot is not ready")
					}
				}
			}
			for i := 0; i < len(cfg.Plays) && play == nil && len(cfg.Hand.Names) == 0; i++ {
				p := &cfg.Plays[i]
				if used[p.ID] || pair[0].Plays[p.ID] != "ready" || pair[1].Plays[p.ID] != "ready" {
					continue
				}
				for j, first := range pair[0].Hand {
					if j >= len(pair[1].Hand) || j >= len(cfg.Hand.Clicks) {
						continue
					}
					second := pair[1].Hand[j]
					if first.Status == "candidate" && second.Status == "candidate" && first.RunnerDistance >= 0 && second.RunnerDistance >= 0 && first.CardID == p.CardID && second.CardID == p.CardID {
						play, slot = p, j
						break
					}
				}
				if play != nil {
					break
				}
			}
			if play == nil {
				if err = record("no_verified_safe_play", pair[:], nil, nil, nil); err != nil {
					return err
				}
				input := observedMatchInput{Name: "pass", Action: Action{Kind: "hold", X: cfg.Pass.X, Y: cfg.Pass.Y}}
				if err = send(pair, input); err != nil {
					return err
				}
				pair, err = await("", []string{"passed", "opponent"}, []string{"turn", "transition"})
				if err != nil {
					return err
				}
				if err = confirm("pass", pair); err != nil {
					return err
				}
				pair, err = await("", []string{"mulligan", "victory", "defeat", "draw"}, []string{"passed", "opponent", "transition"})
				if err != nil {
					return err
				}
				break
			}
			used[play.ID] = true
			card := cfg.Hand.Clicks[slot]
			selectInput := click("select_card", card.X, card.Y, play.ID)
			selectInput.Slot, selectInput.Identified = slot, identified
			if err = send(pair, selectInput); err != nil {
				return err
			}
			pair, err = await(play.ID, []string{"selected"}, []string{"ready"})
			if err != nil {
				return err
			}
			if err = confirm("select_card", pair); err != nil {
				return err
			}
			if err = send(pair, click("place_card", play.Row.X, play.Row.Y, play.ID)); err != nil {
				return err
			}
			pair, err = await(play.ID, []string{"placed", "ended"}, []string{"selected"})
			if err != nil {
				return err
			}
			plays++
			if err = confirm("card_play", pair); err != nil {
				return err
			}
			if pair[1].Plays[play.ID] == "placed" {
				if err = send(pair, click("end_turn", cfg.EndTurn.X, cfg.EndTurn.Y, play.ID)); err != nil {
					return err
				}
				pair, err = await(play.ID, []string{"ended"}, []string{"placed"})
				if err != nil {
					return err
				}
			}
			if err = confirm("turn_end", pair); err != nil {
				return err
			}
			pair, err = await("", []string{"turn", "mulligan", "victory", "defeat", "draw"}, []string{"opponent", "transition"})
			if err != nil {
				return err
			}
		}
		if pair[1].Phase == "mulligan" {
			continue
		}
		if plays == 0 || round < 2 {
			if err = record("result_unqualified", pair[:], nil, nil, nil); err != nil {
				return err
			}
			return fmt.Errorf("result lacks confirmed card play and multiple rounds")
		}
		if err = record("result_confirmed", pair[:], nil, nil, nil); err != nil {
			return err
		}
		if cfg.Exit != nil {
			if err = send(pair, click("exit_result", cfg.Exit.X, cfg.Exit.Y, "")); err != nil {
				return err
			}
			pair, err = await("", []string{"training"}, []string{pair[1].Phase, "transition"})
			if err != nil {
				return err
			}
			if err = confirm("exit_result", pair); err != nil {
				return err
			}
		}
		return record("match_completed", pair[:], nil, nil, nil)
	}
}
