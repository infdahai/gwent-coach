# Training autoplay status

Status: **not qualified**. Phase recognition and a navigation prototype exist, but autonomous game input is deliberately disabled pending on-target validation and safeguards described below. No autonomous training match was started. Manual bootstrap and deck inspection do not count as autonomous trials.

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

Thirty-two local images were classified correctly after choosing regions and thresholds: three each for result, rewards, Bearly-selected mode carousel, main menu and collection, 15 deck-edit images rejected as unknown, and one archived frame each of training versus computer and training versus player. This includes the seven template images used to build the model, and adjacent frames from the same capture batches; it is not a held-out accuracy estimate. Training-selection samples are historical single frames, not validated across animation or interface changes. Account-bearing evidence, configuration and deck manifests remain local under ignored `dist/navigation/`.

The experimental state machine records intent before delivery, requires fresh observations and two matching semantic postconditions, and halts on unknown or unresolved outcomes. Its durable per-account interlock is acquired before navigation and is never automatically cleared, including on success; a new output or session directory cannot bypass it. Model bytes are bound to an explicit SHA-256, which proves identity only, not semantic validity. Generic carousel navigation is refused. Manual bootstrap moved from result to rewards to mode selection to main menu and then deck collection; no GG was sent. Cursor hover and bounded scroll were used only for manual deck inspection. Game input ownership was then handed to another worker. **The Windows navigation entry point remains disabled** until the specific model, target layout, interlock and input sequence have been validated on the target. Cursor checks cannot be atomic with Windows input delivery and do not protect against all concurrent desktop input. The prototype is not autonomous navigation evidence. A menu-phase label alone does not recognize match outcome, scores or GG state.

## Remaining blockers

There is no validated live card recognizer, autonomous match policy, or automatic GG handler. The Enslave #4 saved deck was visually audited as 25 cards across 19 IDs; Carapace #5 and Guerrilla Tactics #6 were also transcribed locally. Saved-deck counts do not establish full account inventory. The collection filter initially showed all cards; a later manual input selected Owned. None of these decks has yet been established as a suitable low-interaction, fully supported training deck. The native engine covers only six card definitions; do not substitute unsupported effects with no-ops.

The next implementation must establish fresh phase-specific evidence and an owned low-interaction deck before initiating a timed trial. Keep selection, placement, target selection, settling, end-turn and pass distinct. A delivered input with unknown outcome blocks another input until reconciled; merely taking a new screenshot must not authorize a semantic retry. Whole-screen equality is unsuitable for the animated client and is not used by this sampler. The old two-minute manual input reference limit is still present and is not sufficient for autonomous action authorization.

## Verification

With existing card snapshots linked locally (not redistributed): `go test ./...`, `go vet ./...`, `GOOS=windows GOARCH=amd64 go build ./...`, and `GOOS=windows GOARCH=amd64 go vet ./...` passed. Sampling tests cover invalid settings, stop-file requirements, JPEG/PNG encoding, aspect ratio, nonzero source bounds and no upscaling. Real sampling measurements above were collected using the GUI-subsystem Windows build. Live focus-loss, interval cancellation, and resized-reference rejection were not exercised in this batch.
