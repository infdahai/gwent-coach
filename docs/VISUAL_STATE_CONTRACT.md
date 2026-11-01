# Visual evidence and public-state estimation

The state-estimation module separates UI stage recognition from game facts. It is in process and offline-capable; the module has no capture, filesystem, game-input or loop lifecycle dependency. The integration adapter now connects complete estimates to `LocalLoop` and maps partial estimates to `ErrUnstableVisual`.

## Contracts

- `stateestimate.FrameRef` identifies one real capture, its match, local player, native geometry and capture time. The capture owner supplies these; rereading a file does not create a new capture.
- `stateestimate.Evidence` contains a UI `Stage`, an immutable `CalibrationID`, and independent `Facts` groups: round/wins, turn/pass flags, score/counts, visible cards/hand order, and outcome. Nil means unknown. An explicit zero count, false flag or empty card set is not unknown. Stage labels, predictions and input receipts cannot fill missing facts.
- `stateestimate.Estimate([2]Evidence, now, maxAge)` checks distinct ordered captures, matching session context/geometry/calibration, and freshness. Each field group survives only when both frames agree. Missing or conflicting groups stay nil. The estimator is stateless and never carries old facts forward.
- `Observation.View` is nil until all native-v1 public-state groups agree, card identities are resolved, and score/card/hand invariants hold. `Eligible` additionally requires the agreed stage `turn`, local turn ownership, and no local pass, round transition or terminal result. This is a decision precondition, not authorization to send input.
- `Observation.StateKey` hashes semantic facts, stage, player, match and calibration. It excludes capture IDs/timestamps and normalizes visible-card enumeration order. Hand order remains significant. Becoming unknown changes the key. The loop owns revision numbering; the constructed `rules.View.Revision` remains zero.

Incomplete observations are successful estimator results with `View == nil`. Invalid/stale capture metadata and inconsistent complete state return errors. Known partial facts remain available for diagnostics, but may not be converted to a zero-valued `rules.View` or used as an action confirmation. A known outcome can be reported as a partial fact even when a results screen does not expose enough information for a complete view.

## Restricted calibration adapter

`observe.NewCalibratedEvidenceReader(EvidenceCalibration)` copies and hashes one immutable configuration. `Read(FrameRef, image.Image)` runs all models on the same native image without encoding or decoding it.

The UI stage has its own model. Every optional `FactCalibration[T]` has a separate model and labelled values for just one field group. For example, two board labels can supply different cards and scores while the UI stage stays `turn`. There is no `States[phase] -> View` lookup. An absent model, unknown match, or ambiguous label produces no fact for that group.

Every fact calibration must declare `RequiredRGB` regions. Construction rejects any label or variant that does not require all of those regions under their specified thresholds. This closes the structural variant bypass, but it does not establish that the artwork, numbers or labels are correct. Operators must audit that the required regions constrain all fields claimed by the group. This is a bounded template implementation, not OCR, general board reconstruction, or a proof that unseen configurations will be rejected. A future recognizer can implement the same Evidence contract without using these templates.

The reader copies configuration at construction and does not share mutable card slices with callers. The frame pixels must remain immutable during `Read`. Evidence retains frame references and a configuration hash; detailed model diagnostics and raw image retention remain the recorder's responsibility.

## Integration sequence

1. Construct one reader per pinned local configuration. Replace capture-to-JPEG-to-readback with a native in-memory frame when the capture owner is ready; this patch does not change that owner.
2. Read two independently captured frames, then call `Estimate` with the current clock and explicit age budget. Offline replay uses recorded frame times and an explicit replay clock.
3. Preserve `Observe(ctx) (SemanticObservation, error)` by mapping only a complete result. Copy `*View`, `Facts.Cards.HandOrder`, frame/source metadata, stage and eligibility. Keep `CalibrationID`/`StateKey` bound to the snapshot or pin the configuration for the entire loop attempt. Map incomplete evidence to the loop's unknown/unstable path; do not clear a pending action or replay it.
4. Keep rules legality, action-specific postconditions, execution-time fresh capture and input interlocks downstream. `StateKey` changes describe observations; they do not prove that a particular action caused the change. Observed game events must be independently checked against the before/after facts, not inferred from an input receipt.

The integration config selects a pinned `EvidenceCalibrationPath` and `EvidenceCalibrationSHA256`; the calibration's stage model must equal the input guard model. This mode rejects static `States` and implicit deck-audit assertions. `Interactions` may provide input geometry and intermediate phase names, never a View. `SemanticObservation.CalibrationID` and `StateKey` participate in the lifecycle fingerprint. Existing static scenarios remain available only through explicit `LegacyScenarioStates: true`; they are restricted fixtures, not a dynamic state estimator. Existing hand scanning and board-recognition components can progressively supply Facts, replacing one calibrated group at a time. Additional ruleset fields (such as deck-audit metadata) require an explicit evidence contract before they may be treated as known.

## Offline calibration construction

`coach -build-evidence manifest.json -evidence-output new-calibration.json` builds an offline runtime configuration and exclusively creates the output. The manifest contains `StageModel` (path relative to the manifest), `StageSHA256` (lowercase SHA-256 of that file), and optional `Round`, `Turn`, `Totals`, `Cards`, `Outcome`, `Targets` (an array), and `Scalars` (a name-to-group map). Each group contains `Labels` (a BuildPhases label manifest path relative to the builder manifest), `Values` (a map from each sample label to its complete typed fact or target value), and `RequiredRGB` (the runtime ROI and MaxError objects). Images are relative to their label manifest. Values must explicitly include every field, fixed-array element, and collection; an empty array is explicit, null is not. Each group needs its own independently verified labels and RGB regions. Missing groups remain unknown. The builder validates structural compatibility, not the truth of labels or deployment readiness; it does not create capture metadata or send input.

## Offline checks

`go test ./internal/stateestimate` checks same-stage/different-board observations, unknown propagation, conflicts, stable semantic keys, hand order, stale/mixed evidence, unresolved identities and inconsistent scores.

`go test ./internal/observe -run '^TestCalibratedEvidence'` exercises image -> independently calibrated facts -> state estimation, missing/ambiguous fact models, missing identity anchors in variants, and configuration/output ownership. Images are generated synthetic fixtures; these tests do not establish real-game recognition accuracy and require no card snapshot download.
