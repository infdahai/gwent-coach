# Training autoplay status

Status: **not qualified**. The current executable collects observations; it does not independently play a match. No new training or online match was started during this implementation. The existing results screen was observed without input. Prior manually driven matches do not count as autonomous trials.

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

## Remaining blockers

There is no runtime phase/card recognizer, autonomous policy, semantic action verification loop or automatic GG handler. The prior training deck was not completely exported or audited. The native engine covers only six card definitions; that does not support the observed prior training deck or prove a legal owned deck can be assembled from those six definitions. Do not replace unsupported abilities with no-ops or choose a deck from unverified ownership assumptions.

The next implementation must establish fresh phase-specific evidence and an owned low-interaction deck before initiating a timed trial. Keep selection, placement, target selection, settling, end-turn and pass distinct. A delivered input with unknown outcome blocks another input until reconciled; merely taking a new screenshot must not authorize a semantic retry. Whole-screen equality is unsuitable for the animated client and is not used by this sampler. The old two-minute manual input reference limit is still present and is not sufficient for autonomous action authorization.

## Verification

With existing card snapshots linked locally (not redistributed): `go test ./...`, `go vet ./...`, `GOOS=windows GOARCH=amd64 go build ./...`, and `GOOS=windows GOARCH=amd64 go vet ./...` passed. Sampling tests cover invalid settings, stop-file requirements, JPEG/PNG encoding, aspect ratio, nonzero source bounds and no upscaling. Real sampling measurements above were collected using the GUI-subsystem Windows build. Live focus-loss, interval cancellation, and resized-reference rejection were not exercised in this batch.
