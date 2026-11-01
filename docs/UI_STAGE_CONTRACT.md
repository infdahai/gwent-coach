# Independent UI stage evidence and events

`internal/uiphase` has only standard-library dependencies. It contains the existing `PhaseModel` implementation and a small ordered-frame recognizer. It knows nothing about players, rules, strategy, game state, input delivery or Windows. `observe.PhaseModel`, `Phase`, `Anchor` and `PhaseScore` remain type aliases; file loading/building/replay remain in `observe`. Model JSON and existing matching behavior are unchanged.

## Input and output

Construct `uiphase.NewRecognizer(model, streamID, maxAge)` once for one stream and pinned model. Construction validates and copies the model. Stream IDs are opaque task/capture-context IDs and can describe menus before a game exists. Calls to one recognizer must be serialized.

`Observe(Frame, now) (Event, error)` accepts a native-sized immutable image plus `FrameRef` containing capture ID, stream ID, capture timestamp and dimensions. Capture IDs must name actual distinct captures; changing the ID or timestamp when rereading the same screenshot does not create new evidence. The capture owner is responsible for their authenticity. `now` is explicit so offline replay can use a recorded clock.

- `Event.Evidence` is the current single-frame classification: bound frame metadata, canonical model fingerprint, raw phase, per-phase scores, and recognition duration. Scores retain the existing model error semantics; they are not probabilities.
- `Event.Phase` is the effective stage, or `unknown`. It is confirmed only after two consecutive distinct, ordered, still-fresh frames agree on a non-unknown phase. `Confirmed` and `Sources` expose that evidence pair.
- `Previous` and `Changed` refer to the effective stage. On the first different or unknown frame the effective stage becomes unknown immediately; the recognizer does not carry a previously confirmed stage through animation. A later matching pair can establish a new stage. These are observational events, not evidence that any input caused a transition.
- A missing image, invalid native geometry, foreign stream, repeated previous capture ID, non-increasing capture time, stale or future frame returns an error with no usable event and breaks confirmation continuity. The last valid capture timestamp is retained, so an old capture cannot be replayed after an error. A long inter-frame gap requires a new pair even if the current frame is fresh.

`RecognitionDuration` covers only `PhaseModel.Recognize`, excluding capture, image IO, temporal processing and input. `Fingerprint` hashes canonical validated model JSON; it is distinct from a digest of arbitrary on-disk JSON bytes.

Unknown and multi-phase ambiguity retain the existing fail-closed matching behavior. Stage events do not authorize game input or supply any field of a game-state `View`.

## Connection to state estimation

`CalibratedEvidenceReader.ReadWithStage(frame, pixels, event.Evidence)` consumes the raw stage evidence without classifying the UI again. It requires the same capture ID, timestamp, native geometry and pinned stage-model fingerprint. In this game-specific adapter the stage stream ID must equal the frame's match ID; the independent stage package itself has no such restriction. Keep frame pixels immutable and pass the exact image underlying the evidence. This is an in-process trusted-producer contract, not verification of arbitrary serialized claims.

Use `Event.Phase`/`Confirmed` for task-stage monitoring. Use `Event.Evidence` for each frame passed to the game-fact reader, then call `stateestimate.Estimate` on the two resulting pieces of evidence. The estimator already requires two-frame agreement, so consuming raw stage evidence avoids requiring another two confirmation frames. `Read` remains available for older callers.

A confirmed stage can coexist with unknown game facts: the state estimator still returns no complete view and cannot mark it eligible. Likewise, the same stage can accompany different cards, scores and state fingerprints. Neither stage events nor their `Changed` flag confirm a pending game action; action-specific postconditions remain downstream.

The integration worktree's Windows capture path constructs one recognizer, decodes each image once, and retains its `Event` on the frame. The evidence observer consumes `ReadWithStage` before mapping a complete estimate into `LocalLoop`. The shared implementation preserves `saturated`, `desaturated` and `green-coverage-v1`; diagnostics include variant, mode and feature. `TrainingSession` consumes confirmed stage pairs for navigation while retaining separate input receipts.

## Offline verification

`go test ./internal/uiphase` covers confirmation, stage transitions, immediate unknown propagation, ambiguity, expired predecessors, metadata errors, continuity breaks and model ownership. `go test ./internal/observe -run 'TestCalibratedEvidence|TestPhase|TestAnchorPixels|TestBuildPhases|TestGreenCoverage|TestRequiresRGBAnchor|TestReplay'` checks compatibility and the stage-to-state connection, including mismatched frame/stream/model rejection. These use synthetic offline images and do not measure live classification accuracy.
