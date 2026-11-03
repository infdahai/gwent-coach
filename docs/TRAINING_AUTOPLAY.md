# Training autoplay status

Status: **experimental; not qualified**. Windows training input, a partial rules-backed decision loop, phase navigation and mulligan transitions have been exercised on the target desktop. No uninterrupted complete match has been qualified. The older prototype and measurements below describe their state at the time and are retained as historical evidence.

## Module boundaries (2026-11-03)

Implementation is split across the following files under `internal/observe/`:

| File | Responsibility | Owner |
|------|---------------|-------|
| `observed_match.go` | Platform-independent runner logic: `runObservedMatch`, frame pair await, turn loop, identity/play selection, result qualification | runner (play agent) |
| `observed_match_config.go` | `ObservedMatchConfig`, `LoadObservedMatchConfig`, config validation, safe unit allowlist, OCR name matching (`namedCard`), art guard | config (hand agent) |
| `observed_match_windows.go` | Windows adapter: `RunObservedMatch`, capture, send, identify (OCR hover), progress, `ExecuteContext` bridge | Windows wiring (this file) |
| `observed_match_test.go` | Platform-independent runner tests (including OCR identity tests via `matchTestHost`) | runner (play agent) |
| `observed_hand*.go` | New hand-identity helper `loadObservedHand` (in progress) | hand agent |
| `observed_play*.go` | New play-selection helper (in progress) | play agent |
| `observed_preflight*.go` | Preflight validation command and runtime-path checks (in progress) | preflight agent |
| `cmd/`                   | CLI entry points: `-observed-match`, `preflight-observed`, control session | preflight/Win wiring |

Shared types (`ObservedMatchConfig`, `ObservedPlay`, `ObservedHandConfig`, `ObservedCardName`) remain in `observed_match_config.go`; the config agent may add `loadObservedHand`. No code is duplicated. Independent verification entry: `go test -count=1 ./internal/observe` runs the platform-independent suite; `GOOS=windows GOARCH=amd64 go test -c` compiles the Windows adapter without exercising it. Live Windows qualification requires the actual target desktop and uninterrupted full matches.

## Constrained observed match runner

`coach.exe -observed-match match.json -out <new-output-directory>` runs one ordinary versus-computer match entirely in the Windows interactive process. The resident host also accepts this file through its existing `-session-config` option; start it once with `coach control run-session` and inspect `control status`, `control receipt`, and `control logs`. The Mac client may inspect logs; all recognition, decisions and inputs run locally on Windows. The existing native rules/session mode remains available separately.

This path uses calibrated UI evidence without constructing a full rules-engine state. Card identity follows one of two independent paths:
- **Art match (standard):** The first unused safe play profile whose `ready` label agrees in two fresh frames and whose card art agrees at the same calibrated hand slot in both frames. Slot identity uses the existing card-art scanner (`internal/observe/hand_scan.go`); other hand slots may remain unknown.
- **OCR name (Names mode):** Configured tooltip card names (`ObservedCardName`) are matched against Windows OCR output after hovering each slot. This path skips the art scanner and requires `cfg.Hand.Names` to be non-empty with at least two competing names. The hover capture and OCR run in the `identify` callback (`observed_match_windows.go`). Identity is established on the hovered frame and guarded at click time by a lifecycle `turn` re-check and the play model's `ready` label on the refreshed pair.
The click follows the recognized slot, so a shuffled order does not require an exact whole-hand template. Each profile is consumed once per match. If none is verified, it passes. This is a conservative execution policy, not a competitive strategy or general hand recognizer. A pass-only match is explicitly unqualified. Completion requires a confirmed placement, at least two observed round starts, and two matching result frames; starting on a result screen never qualifies. An optional direct result-to-training exit is separately confirmed.

The fixed allowlist (`safeObservedUnit` in `observed_match_config.go`) contains units with no deploy target or choice: Pavko Gale (201676), Hawker Smuggler (142315), Antherion (203073), the movement sentry (200039), Naiad Fledgling (203152), Mahakam Defender (142306), deploy-effects-resolve-without-choice cards: Svalblod Priest (203256), Roach (132403), Mahakam Volunteer (200042); and summon/passive-deploy cards: Eskel (112202), Vesemir (112203), Decoy (201559), Blue Stripes Commando (203089).

Pass uses a fixed 1.5-second left-button hold; EndTurn is a plain click. The
observed-match runner already uses `Action.Kind: "hold"` for pass and `Action.Kind:
"click"` for end-turn, matching the control CLIs `coach control hold` and
`coach control click` respectively. Passive effects and orders remain game-owned; the policy does not predict, disable, or activate them. Re-audit this list if the game's card definitions change. Other units, leader actions, targets, tutors and menus stop outside the calibrated path. No LLM service or credentials are needed.

Configuration below illustrates the schema. All points and regions must be measured for the actual 1920×1080 client; the example play coordinates are **not calibration**. Replace both digests with SHA-256 hashes of the actual model bytes, and use the resident host's absolute stop-file path. Model paths and `StateDir` resolve relative to the configuration file. `StateDir` must be new, with its parent already present. `DeckID` records the audited selected deck; the `training` image anchors must independently establish that deck and versus-computer mode.

```json
{
  "Kind": "observed-match-v1",
  "ID": "ordinary-match-001",
  "DeckID": "audited-selected-deck",
  "ModelPath": "lifecycle.json",
  "ModelSHA256": "<sha256-of-lifecycle-model>",
  "StopFile": "<absolute-host-stop-file>",
  "StateDir": "attempt-001",
  "TimeoutMS": 900000,
  "TransitionTimeoutMS": 90000,
  "UnknownTimeoutMS": 5000,
  "Start": {"X": 790, "Y": 555},
  "FinishMulligan": {"X": 870, "Y": 1020},
  "ConfirmMulligan": {"X": 892, "Y": 635},
  "Pass": {"X": 1845, "Y": 540},
  "EndTurn": {"X": 1845, "Y": 540},
  "Hand": {
    "Database": "cards.db",
    "Templates": "hand-templates.json",
    "TemplatesSHA256": "<sha256-of-hand-template-file>",
    "Slots": [{"x":450,"y":900,"width":100,"height":100}],
    "GuardSlots": [{"x":450,"y":850,"width":100,"height":100}],
    "Clicks": [{"X":500,"Y":980}],
    "MaxDistance": 4,
    "MinGap": 3
  },
  "Plays": [{
    "ID": "defender-empty-row",
    "CardID": 142306,
    "ModelPath": "defender-play.json",
    "ModelSHA256": "<sha256-of-play-model>",
    "Row": {"X": 900, "Y": 700},
    "BoardROI": {"Min": {"X": 850, "Y": 650}, "Max": {"X": 950, "Y": 750}}
  }]
}
```

Omit `Exit` to finish at the observed result. Supply `"Exit":{"X":...,"Y":...}` only for an independently verified direct return to `training`; rewards or other intervening menus are unsupported. This runner does not claim GG, score recognition, or strategic effectiveness.

Required local assets:

- **Lifecycle model:** `training`, `mulligan`, `mulligan-confirm`, `turn`, `passed`, and at least one of `victory`, `defeat`, `draw`. Add all result labels actually expected. Calibrate `opponent` for opponent turns and `transition` for non-actionable loading/round animations. Unknown frames wait at most `UnknownTimeoutMS`; known wait phases use `TransitionTimeoutMS`. `turn` must mean an eligible card-play/pass state, not merely a green UI region or an end-turn-only state.
- **Hand identity assets:** versioned `cards.db` and a SHA-256-pinned `cardmatch.File` template index, created using `docs/CARD_LOOKUP.md`. Include safe-card images plus competing other card IDs and independently validated pose variants. `Slots`, `GuardSlots` and `Clicks` are equal-length, same-index lists of 1–10 native art regions and input points. Provide all ten calibrated opening slots for shuffled opening coverage; the one-slot JSON above only illustrates the schema. `Slots` describe unhovered art, while `GuardSlots` describe that card's art after hovering its click point. A click must lie inside its original art region. The post-hover guard rejects an unknown or changed identity. Templates and distance/gap thresholds must be validated against nonmatching cards, not just positive crops. No complete known hand or card count is inferred. Different hand sizes or poses require measured slot coverage; the scanner does not discover card rectangles.
- **One or more play models:** required `ready`, `selected`, `ended`; optional `placed`. Every phase and variant must contain an exact RGB anchor for `BoardROI`, plus independent interaction/turn anchors. The row click must fall within that region. `ready` proves an empty, legal destination. `selected` proves the card-selection/placement interaction, with that destination still empty. `placed` proves the expected known card newly occupies that board region, with an active end-turn control. `ended` proves that same new board card and that the turn has already ended. The latter permits automatic turn handoff without another click. A turn-button change alone cannot confirm placement. Do not bind these models to the identities or exact arrangement of the remaining hand: hand identity comes from the independent slot scanner before selection, and placement comes from the empty-to-known-card board change afterward.
- **Independent calibration evidence:** use native-size PNG frames and the existing `-build-phases`, `-recognize`, `-diagnose-phase`, and `-replay-phases` tools. Test held-out positive frames and negative frames including different cards, hand layouts, an already occupied destination, hover/selection animations and target overlays. Do not relabel old scenario states or use the same crop for both precondition and placement. Card/layout variants need their own independently verified evidence; a hash pins bytes but does not validate their meaning. Images, models and run archives stay private and uncommitted.

Before observed card selection, the Windows input adapter waits 900 ms for hovered art to settle, checking cancellation, STOP and window/deadline guards between waits of at most 50 ms. This stays within the original two-second input deadline; a late guard refuses the click rather than extending that deadline. The adapter then checks the hovered card identity, the play model and the lifecycle `turn` label again immediately before card selection. Other actions use their observed phase's fresh model guard. Foreground, desktop, session, geometry, cursor, Ctrl/F8, STOP and context cancellation protections remain active. Two distinct increasing capture identities/timestamps are required at each confirmation; both must be fresh and captured after delivery. No uncertain input is replayed. The existing shared training interlock remains held after completion or failure, and starting another directory cannot bypass it. Old pending inputs must be reconciled before launching; this runner does not clear old claims or offer a resume shortcut.

The private `StateDir/events.jsonl` records executable/config/model identities, observed frames, synced intent, API delivery receipt, confirmation, round number, confirmed play count, result and failure reason. `result_confirmed` records actual observed result evidence; `match_completed` appears only after the qualification gates and any requested exit. A stopped process with a pending intent is unresolved even if a transport receipt exists. The resident command log shows compact action/confirmation events and an observation heartbeat at most every five seconds, keeping the bounded host log readable through the result. Full capture/input diagnostics remain in private `StateDir/native.log`, and every observed frame remains in the canonical JSONL ledger.

Offline verification covers a two-round match with actual selection/placement steps, manual and automatic turn endings, optional exit, cancellation, missing/uncertain receipts, repeated evidence, incomplete placement and result-only/pass-only rejection. Windows GUI and Mac builds are available from the task build output. **Live autonomous completion remains pending calibrated assets and an uninterrupted Windows run.** These synthetic tests do not qualify a real match or replace the broader repeated-trial acceptance baseline below.

## Live integration, 2026-11-01

The Windows resident process can navigate calibrated training phases, finish a mulligan, verify visible hand slots in two frames, select supported legal actions through the decision service, deliver guarded inputs and check action-specific postconditions. Unknown slots remain unknown. The phase and card templates are local to the observed client and are not a general recognizer.

In a real decision-loop run, pass was selected after a 249.110 ms observation and 92.732 ms observation-to-decision interval; the hold took 3032.310 ms. A prefetched frame captured before delivery was discarded at age 3434.935 ms. The same process observed the next round and sent the finish-mulligan and confirmation clicks once each (1698.197 ms and 1726.915 ms). Two later frames showed the second-round board. The loop then stopped on a phase outside its calibrated model; these events do not establish a full autonomous match.

Earlier verified plays include wolf pack, a tactical advantage order, mage assassin and Oxenfurt scholar. Some intermediate inputs were reconciled from subsequent frames after automatic confirmation stopped; the action log distinguishes delivery from confirmation. The scholar play was observed to hand over the turn automatically. The adapter now waits for either a verified placed state or a verified automatic turn transition, and does not send a second end-turn click in the latter case. Tests cover that branch and waiting through transient unknown frames while an action remains pending.

Evidence and calibration remain on the target under `D:\\gwent-coach-loop-review-20261101-0316\\calibration`; the pass and round transition are recorded in `cycle-pass-032`, `cycle-pass-output-032`, and `cycle-pass-reconciliation-032.json`. These files include account-bearing captures and are not distributed. The durable interlock is archived only after reviewing the event ledger, action records and subsequent frames.

## Offline orchestration integration

`LocalLoop.Policy.Start` receives the lifecycle's monotonic snapshot version. The strategy adapter fixes the deadline for that version, polls completed proposals and explicitly falls back before the input deadline, reserving five seconds in the Windows host. Without an asynchronous policy it uses fallback immediately. `rules.Evaluator` supplies synchronous candidate generation and `ValidateAction`; the Windows host does not start the older rules strategy worker. Existing unknown-card and partial-effect restrictions remain in force.

The durable stream at `<StateDir>/match/events.jsonl` uses `matchlog.Event`: `action_intent` → per-step `input_intent`/`input_receipt` → `action_delivered` → `action_confirmed`. Every boundary is synced. `DeliveredAt` comes from the actual input API return, before the settling wait or screenshot; event `Sequence` is independent of semantic `StateVersion`. Failure or partial delivery cannot produce an action confirmation. Natural `ui_stage_observed` and `result_observed` events do not create input receipts. The older diagnostic stream remains separate.

`matchlog.ConfirmedTrainingEvents` selects gameplay confirmations joined to their original intent and complete delivery. Navigation, unconfirmed inputs and predictions are excluded. `TrainingSession` serializes entry/mulligan navigation, a supplied `LocalLoop` match runner, confirmed result exit and the next match. Its two-match synthetic integration test runs these components together and verifies one confirmed gameplay sample per match. This is an offline orchestration proof, not two live autonomous matches.

`matchlog.Resume` rejects terminal or torn logs and preserves event sequence. Journal recovery restores the original action, semantic snapshot, ordered input steps and receipts. A fully delivered pending action resumes observation/confirmation without policy selection or replay. Navigation recovery also retains the original successor even if the supplied plan changes. Missing receipts or incomplete steps remain unresolved and require evidence-based reconciliation. `ResumeJournal: true` in the Windows host opens the same attempt and skips neutral hover, prelude and mulligan inputs; it requires a fully delivered pending action and the existing input interlock must still be acquired. The older manually calibrated intermediate recovery paths now require an explicit original `ResumeAction`, not a fresh policy decision.

The new strategy, receipt, stage and state-estimation integrations have not been deployed or exercised on Windows after the user requested offline-only work. `coach -native-session session.json` now constructs the Windows host, acquires the shared interlock once, performs calibrated session navigation, and calls the existing native loop with the session-owned journal and per-match ID. That inner loop skips duplicate startup navigation and uses the same capture/guard/receipt implementation. Between-round mulligans also use the session navigation controller. Each completed match writes `training.jsonl` beside its canonical `events.jsonl`, containing confirmed gameplay samples only. The common configuration runner and two-match path are exercised with an offline host; Windows host wiring is cross-built, not live-qualified. General recognition and dynamic public-state coverage remain outstanding.

Minimal session configuration (paths are relative to this file; provide an actual SHA-256 and calibrated coordinates):

```json
{
  "ID": "training-session-001",
  "StateDir": "session-001",
  "LoopConfig": "loop-profile.json",
  "LoopConfigSHA256": "<sha256-of-loop-profile-json>",
  "StartMatch": 1,
  "Matches": 2,
  "TransitionTimeoutMS": 90000,
  "Entry": [
    {"From":"training","To":"mulligan","Input":{"kind":"click","x":790,"y":555}},
    {"From":"mulligan","To":"mulligan-confirm","Input":{"kind":"click","x":870,"y":1020}},
    {"From":"mulligan-confirm","To":"turn","Input":{"kind":"click","x":892,"y":635}}
  ],
  "Exit": [{"From":"result","To":"training","Input":{"kind":"click","x":960,"y":1027}}]
}
```

The loop profile supplies the pinned phase/evidence models, catalog and execution geometry. All navigation phases must be in its model; the host validates assets before input. The session supplies match IDs and the durable journal. `ResumeFirst: true` resumes an unfinished match directory at `StartMatch`; completed or torn logs are rejected. A results-to-rewards or other route must be explicitly calibrated in `Exit` instead of assuming the direct transition above. These example coordinates describe a previously measured layout and are not general calibration.

## Acceptance baseline

The eventual start boundary is the verified in-game Training / versus-computer screen with a fully audited, owned deck selected. Each trial starts before the program initiates matchmaking and ends only after a recognized result and either verified GG or evidence that the training mode does not offer GG. Navigation, mulligan, card play, target selection, turn transitions and result handling must require no operator input. Online seasonal matchmaking is outside this task.

Qualify only after at least five consecutive complete autonomous trials, with all initiated attempts retained in the denominator, including failures, interruptions, focus loss, timeouts and manual intervention. Require 100% recognized and traceable results and zero unresolved wrong or duplicate inputs. Record wins, draws and losses separately from execution success. A loss may satisfy execution reliability; five losses cannot establish strategic effectiveness. Unknown results never count as successful trials. Insufficient samples remain not qualified.

Every future trial needs an append-only record containing trial ID, executable SHA-256, configuration/deck identity, start/end timestamps, mode evidence, frame references, decisions and their preconditions, delivered events, semantic verification, interruption reasons, outcome, round scores and GG status. Current observation reports are evidence files, not that trial ledger.

## Implemented observation slice

Run inside the Windows interactive session, using the existing deployment procedure:

```powershell
coach.exe -out C:\coach\training-samples -samples 30 -interval 500ms -max-width 960 -jpeg-quality 65 -stop-file C:\coach\PAUSE
```

This is a bounded local loop and sends no input. Each frame has its own directory and report. Sampling stops on the first observation failure or an existing stop file. Existing foreground, desktop and session checks remain in force. The stop file is checked before each frame; cancellation during an interval waits until the next frame boundary. F8/Ctrl checks belong to the input path, not this observation-only loop.

Default image output remains native-size PNG. `-max-width` preserves aspect ratio without upscaling; `-jpeg-quality` selects JPEG instead of PNG. Reports preserve native client geometry separately from encoded `image_size`. Resized reports are refused as input references to prevent accidental use of preview coordinates. Downscaling uses nearest-neighbor sampling and has not been qualified for OCR or card recognition.

`time` records sample start, `captured_at` records completion of capture and post-capture focus validation, and `duration_ms` measures collection plus image encoding/writing up to report serialization. It excludes report serialization, transport, recognition, decision and input delivery; it is not action end-to-end latency. Intervals are minimum start-to-start targets, with no overlapping captures or catch-up bursts.

## Fixed opening experiment

`coach.exe -out <output-dir> -native-opening <config.json>` runs one calibrated opening only. The JSON configuration contains `ModelPath`, `ModelSHA256`, `StopFile`, `StateDir`, and native-pixel `Card`, `Row`, `EndTurn` points as `{"X":...,"Y":...}`. The stop file must be `D:\\gwent-coach-training-local\\PAUSE`; the state directory must be new. The phase model must contain `slice-ready`, `slice-selected`, `slice-placed` and `slice-ended`. The ready model must establish the specific observed opening and the absence of an enemy target; this is not general turn recognition. Provide independently calibrated labels and points; the program does not create or validate calibration. It writes and syncs intent and confirmation events, holds the shared training interlock, and stops on uncertainty. It has a 10-second budget beginning at the first ready frame; it rejects input if the deadline expires before delivery. It cannot interrupt an already executing Windows API call. This fixed sequence does not use the rules decision service, cannot handle other cards or an enemy target, and is not a continuous autonomous match.

## Calibrated hand capture (observation only)

`coach.exe -out <output-dir> -observe-hand <config.json> -stop-file <pause-file>` captures two native Windows frames 500 ms apart and compares each explicitly supplied art rectangle against a local template index. Add `-samples 30 -interval 1s` to repeat in one process with the index loaded once; each JSONL result includes the two capture/decode durations, recognition duration and total scan duration in milliseconds. Total includes the 500 ms separation, and no decision or input occurs. It prints report paths and one unvalidated candidate or unknown per slot; it sends no input, detects neither the number nor location of cards, and does not establish whose turn it is. Use one configuration for the actual native resolution and observed hand layout, with slots in left-to-right order. Paths are relative to the configuration file unless absolute:

```json
{
  "Database": "cards.db",
  "Templates": "templates.json",
  "Width": 1920,
  "Height": 1080,
  "Slots": [{"x": 100, "y": 900, "width": 80, "height": 100}],
  "MaxDistance": 4,
  "MinGap": 3
}
```

The numbers above illustrate the format only; they are not calibrated coordinates or thresholds. The operator must measure art regions and collect independently labelled gameplay samples, including nonmatching cards and ambiguous art, for each supported hand size, animation, client scale and layout. Build the database and templates using `docs/CARD_LOOKUP.md`. Two-frame agreement requires a competing template and remains unvalidated; a missing card, overlapping artwork or an omitted slot may otherwise go unnoticed. Keep images and templates local. Before enabling live play, independently verify complete public state, turn eligibility, target positions, interaction sequence, delivery and action-specific postconditions.

## Real desktop measurement, 2026-10-31

A single observation-only batch ran on the existing 1920 by 1080 results screen between 13:33:53 and 13:34:08 UTC. All 30 reports succeeded and the scheduled task returned zero. Output was 960 by 540 JPEG at quality 65.

| Measurement | Observed value |
| --- | ---: |
| Frames | 30 |
| Observation errors | 0 |
| Median collection duration | 241.013 ms |
| Nearest-rank p95 collection duration | 271.543 ms |
| Maximum collection duration | 322.391 ms |
| Minimum start interval | 500.315 ms |
| Maximum start interval | 502.080 ms |
| Autonomous training trials initiated | 0 |
| Autonomous trials completed | 0 |
| Match result recognition rate | Not measured |
| Decision-to-verified-action latency | Not measured |

Raw reports and account-bearing screenshots are local only under ignored `dist/training-autoplay/`. Results-screen text was visually checked in one compressed frame; this is not automated OCR verification. No gameplay timer budget was measured.

## Phase recognition and navigation prototype

The recognizer compiles local operator-labelled UI regions into cell-averaged RGB templates and rejects unknown, ambiguous, and mismatched-aspect frames. No account images or templates are distributed. `coach -build-phases labels.json -phase-model model.json` compiles labels; `coach -phase-model model.json -recognize frame.png` evaluates a frame. Labels specify `Width`, `Height`, and `Samples` with `Name`, relative `Image`, and `Anchors` containing `X`, `Y`, `Width`, `Height`, and `MaxError`; coordinates are in the label dimensions. Template thresholds must be calibrated and tested on separate positive and negative frames for the specific UI. Never treat an arbitrary model as authorization for an input.

`coach -phase-model model.json -replay-phases frames.json` evaluates a local manifest of `{"frames":[{"image":"frame.png","expected":"unknown"}]}` and writes one JSON object per frame with classification, scores, correctness, and monotonic recognition duration. Paths are relative to the manifest. This is an offline evaluation only; labels and account-bearing images must remain local. Separate matches and sessions for training and evaluation; self-matches and adjacent frames do not establish accuracy.

Thirty-two local images were classified correctly after choosing regions and thresholds: three each for result, rewards, Bearly-selected mode carousel, main menu and collection, 15 deck-edit images rejected as unknown, and one archived frame each of training versus computer and training versus player. This includes the seven template images used to build the model, and adjacent frames from the same capture batches; it is not a held-out accuracy estimate. Training-selection samples are historical single frames, not validated across animation or interface changes. Account-bearing evidence, configuration and deck manifests remain local under ignored `dist/navigation/`.

The experimental state machine records intent before delivery, requires fresh observations and two matching semantic postconditions, and halts on unknown or unresolved outcomes. Its durable per-account interlock is acquired before navigation and is never automatically cleared, including on success; a new output or session directory cannot bypass it. Model bytes are bound to an explicit SHA-256, which proves identity only, not semantic validity. Generic carousel navigation is refused. Manual bootstrap moved from result to rewards to mode selection to main menu and then deck collection; no GG was sent. Cursor hover and bounded scroll were used only for manual deck inspection during the bootstrap using an experimental build. These action kinds were not supported by the executable at that time. Game input ownership was then handed to another worker. The prototype did not establish autonomous navigation; later live integration is described above. Cursor checks cannot be atomic with Windows input delivery and do not protect against all concurrent desktop input. A menu-phase label alone does not recognize match outcome, scores or GG state.

## Remaining blockers

Card, board and phase coverage remain narrow and tied to local calibration; cross-match recognition, automatic result handling and GG remain unqualified. The Enslave #4 saved deck was visually audited as 25 cards across 19 IDs; Carapace #5 and Guerrilla Tactics #6 were also transcribed locally. Saved-deck counts do not establish full account inventory. The rules engine supports a limited set of exact effects and explicitly restricted contexts; unsupported effects must not be treated as no-ops.

Keep selection, placement, target selection, settling, end-turn and pass distinct. A delivered input with unknown outcome blocks another input until reconciled; merely taking a new screenshot must not authorize a semantic retry. Whole-screen equality is unsuitable for the animated client and is not used by this sampler. The old two-minute manual input reference limit is still present and is not sufficient for autonomous action authorization.

## Verification

With existing card snapshots linked locally (not redistributed): `go test ./...`, `go vet ./...`, `GOOS=windows GOARCH=amd64 go build ./...`, and `GOOS=windows GOARCH=amd64 go vet ./...` passed. Sampling tests cover invalid settings, stop-file requirements, JPEG/PNG encoding, aspect ratio, nonzero source bounds and no upscaling. Real sampling measurements above were collected using the GUI-subsystem Windows build. Live focus-loss, interval cancellation, and resized-reference rejection were not exercised in this batch.
