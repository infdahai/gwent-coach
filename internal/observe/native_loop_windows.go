//go:build windows

package observe

import (
	"context"
	"encoding/json"
	"fmt"
	"image"
	"io"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"gwentcoachmvp/internal/cardindex"
	"gwentcoachmvp/internal/cardmatch"
	"gwentcoachmvp/internal/cards"
	"gwentcoachmvp/internal/matchlog"
	"gwentcoachmvp/internal/rules"
	"gwentcoachmvp/internal/strategy"
)

func RunNativeLoop(out string, logger *log.Logger, configPath string, output io.Writer) error {
	return RunNativeLoopWithStrategy(out, logger, configPath, output, nil)
}

type nativeLoopBinding struct {
	Preflight    bool
	Profile      []byte
	MatchID      string
	Journal      *ActionJournal
	OnResult     func(SemanticObservation)
	OnRoundStart func(context.Context, SemanticObservation) error
}

// RunNativeLoopWithStrategy attaches one optional asynchronous policy to the resident loop.
func RunNativeLoopWithStrategy(out string, logger *log.Logger, configPath string, output io.Writer, selected strategy.Policy) error {
	return runNativeLoopAttempt(context.Background(), out, logger, configPath, output, selected, nil)
}

func runNativeLoopAttempt(ctx context.Context, out string, logger *log.Logger, configPath string, output io.Writer, selected strategy.Policy, binding *nativeLoopBinding) error {
	var cfg NativeLoopConfig
	if binding != nil && len(binding.Profile) > 0 {
		if err := json.Unmarshal(binding.Profile, &cfg); err != nil {
			return err
		}
	} else if err := cardindex.ReadJSON(configPath, &cfg); err != nil {
		return err
	}
	if binding != nil {
		cfg.MatchID = binding.MatchID
	}
	skipInitial := binding != nil || cfg.ResumeJournal
	resolve := func(p string) string {
		if filepath.IsAbs(p) {
			return p
		}
		return filepath.Join(filepath.Dir(configPath), p)
	}
	if cfg.MatchID == "" || (cfg.StateDir == "" && binding == nil) || !strings.EqualFold(filepath.Clean(cfg.StopFile), `D:\gwent-coach-training-local\PAUSE`) {
		return fmt.Errorf("match ID, new state directory and fixed training stop file required")
	}
	cfg.ModelPath = resolve(cfg.ModelPath)
	model, err := loadBoundPhases(cfg.ModelPath, cfg.ModelSHA256)
	if err != nil {
		return err
	}
	var evidenceReader *CalibratedEvidenceReader
	if cfg.EvidenceCalibrationPath != "" {
		if cfg.LegacyScenarioStates || len(cfg.States) > 0 || cfg.StartingDeckAudit != "" {
			return fmt.Errorf("evidence estimator cannot use legacy state or deck assertions")
		}
		evidenceReader, err = loadEvidenceReader(resolve(cfg.EvidenceCalibrationPath), cfg.EvidenceCalibrationSHA256)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(evidenceReader.cal.Stage, *model) {
			return fmt.Errorf("evidence stage and input guard models differ")
		}
	} else if !cfg.LegacyScenarioStates {
		return fmt.Errorf("evidence calibration required; legacy scenario states need explicit opt-in")
	}
	var matcher *cardmatch.Index
	if cfg.LegacyScenarioStates || cfg.Hand.Database != "" {
		version := cardindex.Version{Data: cardindex.DataVersion, Rules: cardindex.RulesVersion}
		db, e := cardindex.Open(resolve(cfg.Hand.Database), version)
		if e != nil {
			return e
		}
		var template cardmatch.File
		if e = cardindex.ReadJSON(resolve(cfg.Hand.Templates), &template); e != nil {
			return e
		}
		matcher, e = cardmatch.New(template, db)
		if e != nil {
			return e
		}
	}
	catalog, err := cards.Load(resolve(cfg.Catalog))
	if err != nil {
		return err
	}
	if cfg.StartingDeckAudit != "" {
		var audit auditDeck
		if err := cardindex.ReadJSON(resolve(cfg.StartingDeckAudit), &audit); err != nil {
			return err
		}
		high, err := startingHighProvisions(catalog, audit)
		if err != nil {
			return err
		}
		for name, state := range cfg.States {
			if state.View.StartingDeckKnown && state.View.StartingHighProvision != high {
				return fmt.Errorf("starting deck evidence disagrees with state %q", name)
			}
			state.View.StartingDeckKnown = true
			state.View.StartingHighProvision = high
			cfg.States[name] = state
		}
	}
	engine, err := rules.New(catalog)
	if err != nil {
		return err
	}
	if cfg.LegacyScenarioStates && len(cfg.States) == 0 {
		return fmt.Errorf("calibrated states required")
	}
	names := map[string]bool{}
	for _, p := range model.Phases {
		names[p.Name] = true
	}
	for name, state := range cfg.States {
		if !names[name] {
			return fmt.Errorf("state %q missing from model", name)
		}
		for _, phase := range state.SelectedPhases {
			if !names[phase] {
				return fmt.Errorf("selected phase %q missing from model", phase)
			}
		}
		for _, phase := range state.AutoEndedPhases {
			if !names[phase] {
				return fmt.Errorf("automatic turn phase %q missing from model", phase)
			}
		}
		for _, phase := range state.TargetPhases {
			if !names[phase] {
				return fmt.Errorf("target phase %q missing from model", phase)
			}
		}
		for _, phase := range state.PlacedPhases {
			if !names[phase] {
				return fmt.Errorf("placed phase %q missing from model", phase)
			}
		}
	}
	for stage, interaction := range cfg.Interactions {
		if !names[stage] {
			return fmt.Errorf("interaction stage %q missing", stage)
		}
		for _, phases := range []map[string]string{interaction.SelectedPhases, interaction.PlacedPhases, interaction.TargetPhases, interaction.AutoEndedPhases} {
			for _, phase := range phases {
				if !names[phase] {
					return fmt.Errorf("interaction phase %q missing", phase)
				}
			}
		}
	}
	for _, transition := range cfg.Prelude {
		if !names[transition.From] || !names[transition.To] {
			return fmt.Errorf("prelude phases missing from model")
		}
		for _, alternative := range transition.Also {
			if !names[alternative] {
				return fmt.Errorf("prelude alternative missing from model")
			}
		}
	}
	if cfg.ResumeCard != "" && (cfg.ResumeBaseline == "" || (cfg.ResumeSelectedPhase == "") == (cfg.ResumePlacedPhase == "") || (cfg.ResumeSelectedPhase != "" && !names[cfg.ResumeSelectedPhase]) || (cfg.ResumePlacedPhase != "" && !names[cfg.ResumePlacedPhase])) {
		return fmt.Errorf("resume calibration missing from model")
	}
	if cfg.ResumeTargetCard != "" && (cfg.ResumeBaseline == "" || cfg.ResumeTargetID == "" || !names[cfg.ResumeTargetPhase] || cfg.ResumeCard != "" || cfg.ResumeOrderCard != "") {
		return fmt.Errorf("mage target recovery calibration invalid")
	}
	if cfg.ResumeOrderCard != "" && (cfg.ResumeBaseline == "" || cfg.ResumeOrderTarget == "" || !names[cfg.ResumeOrderPhase] || cfg.ResumeCard != "") {
		return fmt.Errorf("order recovery calibration invalid")
	}
	if cfg.OrderSelectedPhase != "" && !names[cfg.OrderSelectedPhase] {
		return fmt.Errorf("order selected phase missing from model")
	}
	if cfg.MulliganOnTransition && cfg.MulliganPhase != "mulligan" {
		return fmt.Errorf("round transition requires mulligan phase")
	}
	if cfg.ConfirmationTimeoutMS < 0 || cfg.ConfirmationTimeoutMS > 120000 {
		return fmt.Errorf("invalid confirmation timeout")
	}
	if cfg.MulliganPhase != "" && (!names[cfg.MulliganPhase] || !names[cfg.MulliganConfirmPhase]) {
		return fmt.Errorf("mulligan phases missing from model")
	}
	if cfg.SelectedPhase != "" && !names[cfg.SelectedPhase] || cfg.PlacedPhase != "" && !names[cfg.PlacedPhase] {
		return fmt.Errorf("intermediate phases missing from model")
	}
	if binding != nil && binding.Preflight {
		return nil
	}
	var journal *ActionJournal
	var events io.Writer = output
	if binding != nil {
		journal = binding.Journal
	} else {
		flags := os.O_CREATE | os.O_EXCL | os.O_WRONLY
		if cfg.ResumeJournal {
			flags = os.O_APPEND | os.O_WRONLY
		} else if err := os.Mkdir(cfg.StateDir, 0700); err != nil {
			return err
		}
		file, err := os.OpenFile(filepath.Join(cfg.StateDir, "events.jsonl"), flags, 0600)
		if err != nil {
			return err
		}
		defer file.Close()
		if err := acquireInterlock(`D:\gwent-coach-training-local\training-autoplay-interlock`); err != nil {
			return err
		}
		var matchEvents *matchlog.Writer
		var persisted []matchlog.Event
		if cfg.ResumeJournal {
			matchEvents, persisted, err = matchlog.Resume(filepath.Join(cfg.StateDir, "match"), cfg.MatchID)
		} else {
			matchEvents, err = matchlog.Create(filepath.Join(cfg.StateDir, "match"), cfg.MatchID)
		}
		if err != nil {
			return err
		}
		defer matchEvents.Abort()
		journal, err = RestoreActionJournal(matchEvents, persisted)
		if err != nil {
			return err
		}
		if cfg.ResumeJournal && journal.Pending() == nil {
			return fmt.Errorf("resume requires a fully delivered pending action; inspect original steps")
		}
		events = syncedSliceWriter{file, output}
	}
	capture, err := newNativeCapture(out, logger, cfg, model)
	if err != nil {
		return err
	}
	send := func(ctx context.Context, f loopFrame, kind string, p image.Point, deadline time.Time) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if p == (image.Point{}) || !p.In(image.Rect(0, 0, 1920, 1080)) {
			return fmt.Errorf("invalid action point")
		}
		started := time.Now()
		if err := json.NewEncoder(events).Encode(map[string]any{"status": "intent", "kind": kind, "reference": f.Reference, "time": started.UTC()}); err != nil {
			return err
		}
		err := journal.ExecuteStep(Action{Kind: kind, X: p.X, Y: p.Y, Reference: f.Reference, StopFile: cfg.StopFile, PhaseModel: cfg.ModelPath, ExpectedPhase: f.Phase, phaseModel: model, deadline: deadline, primitiveOnly: true}, func(action Action) error { return Execute(out, logger, action) })
		if err != nil {
			return err
		}
		return json.NewEncoder(events).Encode(map[string]any{"status": "delivered_unconfirmed", "kind": kind, "duration_ms": float64(time.Since(started)) / float64(time.Millisecond)})
	}
	if !skipInitial && cfg.StartNeutralPoint != (image.Point{}) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		frame, err := capture(ctx)
		if err == nil {
			err = send(ctx, frame, "hover", cfg.StartNeutralPoint, time.Now().Add(8*time.Second))
		}
		cancel()
		if err != nil {
			return fmt.Errorf("initial neutral hover unresolved: %w", err)
		}
	}
	if !skipInitial {
		if err := runNativePrelude(ctx, cfg.Prelude, cfg.VerifyOldPrefetch, capture, send, events); err != nil {
			return err
		}
	}
	adapter := &nativeLoopAdapter{cfg: cfg, matcher: matcher, model: model, capture: capture, send: send}
	if evidenceReader != nil {
		adapter.evidenceReader = evidenceReader
	}
	service := rules.NewEvaluator(engine)
	policy, err := NewStrategyPolicy(service, selected, 5*time.Second)
	if err != nil {
		return err
	}
	defer policy.Close()
	if journal.Pending() != nil {
		if err := json.Unmarshal(journal.Pending().Snapshot.State, &adapter.before); err != nil {
			return err
		}
	}
	if !skipInitial && !cfg.MulliganOnTransition {
		if err := adapter.finishMulligan(context.Background()); err != nil {
			return err
		}
	}
	if !skipInitial {
		if err := adapter.resumeSelected(context.Background(), service); err != nil {
			return err
		}
		if err := adapter.resumeOrderSelected(context.Background(), service); err != nil {
			return err
		}
		if err := adapter.resumeMageTarget(context.Background(), service); err != nil {
			return err
		}
	}
	observe := func(ctx context.Context) (SemanticObservation, error) {
		started := time.Now()
		o, err := adapter.observe(ctx)
		logger.Printf("observe elapsed_ms=%.3f eligible=%t error=%v", float64(time.Since(started))/float64(time.Millisecond), o.Eligible, err)
		return o, err
	}
	confirmationBudget := 10 * time.Second
	if cfg.ConfirmationTimeoutMS > 0 {
		confirmationBudget = time.Duration(cfg.ConfirmationTimeoutMS) * time.Millisecond
	}
	var onRoundStart func(context.Context, SemanticObservation) error
	if cfg.MulliganOnTransition {
		onRoundStart = func(ctx context.Context, _ SemanticObservation) error { return adapter.finishMulligan(ctx) }
	}
	if binding != nil && binding.OnRoundStart != nil {
		onRoundStart = binding.OnRoundStart
	}
	var onResult func(SemanticObservation)
	if binding != nil {
		onResult = binding.OnResult
	}
	planner := ActionPlanner{AllowPassWithCards: cfg.PassWhenNoPlayable, Config: cfg}
	return (LocalLoop{Observe: observe, Plan: func(o SemanticObservation, action rules.Action, deadline time.Time) (ActionPlan, error) {
		adapter.mu.Lock()
		defer adapter.mu.Unlock()
		adapter.before = o
		return planner.Build(o, adapter.state, action, deadline)
	}, ObserveTarget: adapter.observeTarget, ExecutePlanned: func(ctx context.Context, input PlannedInput, evidence TargetEvidence, deadline time.Time) error {
		adapter.mu.Lock()
		defer adapter.mu.Unlock()
		if adapter.last.Reference != evidence.Sources[1] {
			return fmt.Errorf("target evidence superseded")
		}
		return adapter.send(ctx, adapter.last, input.Kind, input.Point, deadline)
	}, ObserveAutoEnd: func(ctx context.Context, action rules.Action, after time.Time) (SemanticObservation, bool, error) {
		o, err := adapter.observe(ctx)
		if err != nil {
			return SemanticObservation{}, false, err
		}
		if len(o.Frames) != 2 || !o.Frames[0].CapturedAt.After(after) || !o.Frames[1].CapturedAt.After(o.Frames[0].CapturedAt) {
			return SemanticObservation{}, false, fmt.Errorf("fresh post-input evidence required")
		}
		ended := adapter.confirm(action, o) && (o.Result || o.View.Round != adapter.before.View.Round || o.View.Turn != adapter.before.View.Turn)
		return o, ended, nil
	}, Confirm: func(action rules.Action, o SemanticObservation) bool {
		return adapter.confirm(action, o) && (action.Kind != "play" || o.Result || o.View.Round != adapter.before.View.Round || o.View.Turn != adapter.before.View.Turn)
	}, Decisions: service, Policy: policy, Journal: journal, OnResult: onResult, OnRoundStart: onRoundStart, FilterCandidates: func(o SemanticObservation, candidates []rules.Action) []rules.Action {
		adapter.mu.Lock()
		defer adapter.mu.Unlock()
		var supported []rules.Action
		for _, candidate := range candidates {
			if _, err := planner.Build(o, adapter.state, candidate, time.Now().Add(time.Second)); err == nil {
				supported = append(supported, candidate)
			}
		}
		return supported
	}, AllowPassWithCards: cfg.PassWhenNoPlayable, DecisionBudget: 10 * time.Second, ConfirmationBudget: confirmationBudget, OnDecision: func(action rules.Action, elapsed time.Duration) {
		logger.Printf("decision kind=%s observation_to_decision_ms=%.3f", action.Kind, float64(elapsed)/float64(time.Millisecond))
	}, OnDiscardPredelivery: func(o SemanticObservation, age time.Duration) {
		logger.Printf("predelivery_observation_discarded source=%s age_ms=%.3f", o.Source, float64(age)/float64(time.Millisecond))
	}}).Run(ctx)
}
