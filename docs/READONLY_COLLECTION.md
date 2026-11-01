# Windows read-only collection demo

The collector captures a bounded series of frames from the foreground Gwent window and records experimental numeric candidates. It does not send input or play matches. Run it in the same unlocked interactive Windows session as the restored, foreground game. The existing capture checks reject other sessions, locked desktops and foreground changes.

## Build and configure

From the repository on Windows, build with Go:

```powershell
New-Item -ItemType Directory -Force .\bin | Out-Null
go build -o .\bin\coach.exe ./cmd/coach
```

Save the following as `collector.json`. This example was calibrated against a small set of 1920×1080 frames and contains templates only for 0 and 1. Its coordinates and thresholds may not match another window, resolution, UI or game state.

```json
{
  "Digits": {
    "opponent_score": {
      "ROI": {"Min": {"X": 1815, "Y": 345}, "Max": {"X": 1875, "Y": 400}},
      "Threshold": 140, "CandidateOnly": true,
      "Width": 5, "Height": 7, "MaxDigits": 2, "MaxDistance": 2, "MinGap": 4,
      "Templates": {"0": ["01110110111000110001100011101101110"], "1": ["00111000110001100011000110001100011", "01111001110011100111001110011100000"]}
    },
    "own_score": {
      "ROI": {"Min": {"X": 1815, "Y": 675}, "Max": {"X": 1875, "Y": 730}},
      "Threshold": 140, "CandidateOnly": true,
      "Width": 5, "Height": 7, "MaxDigits": 2, "MaxDistance": 2, "MinGap": 4,
      "Templates": {"0": ["01110110111000110001100011101101110"], "1": ["00111000110001100011000110001100011", "01111001110011100111001110011100000"]}
    }
  }
}
```

## Run

With the game foreground in the interactive session, run the executable from that session. The stop-file argument is required; an existing file at that path pauses capture. Choose a path that does not currently exist.

```powershell
.\bin\coach.exe -out .\runs -collect-readonly .\collector.json -stop-file .\PAUSE -samples 10 -interval 1s
```

The collector accepts 1–100 frames per invocation and intervals of at least 100 ms. It stops on capture or recognition errors. `runs\coach.log` records errors; each invocation creates `runs\readonly-collection-*\recognition.jsonl` and per-frame directories containing a timestamped capture report and JPEG. The `reference` field points to the report, which identifies the screenshot. Runs accumulate across invocations; remove old local runs when no longer needed. Screenshots and reports can contain private information.

On the Mac, copy a run's `recognition.jsonl` locally and summarize it with `go run ./cmd/collection-analysis /path/to/recognition.jsonl`. The JSON output counts frames, candidate values, unknown frames, and reported consecutive candidate agreements for each scalar. It retains the `unverified_candidate_not_for_actions` trust marker; agreement and counts do not validate recognition. The command reads only the JSONL, not the referenced screenshots, and does not train a model or report match results.

Each JSONL line has `captured_at`, `reference`, `scalars`, `unknown`, and the trust marker `unverified_candidate_not_for_actions`. `agreed`, when present, records agreement between consecutive candidate readings; it does not establish correctness. Menus, occlusion, and strokes between sample points can yield false values. These candidates are rejected by the gameplay evidence reader and are not connected to `Facts.Totals` or action selection. Validate frames manually before using them as research data; the demo does not provide reliable score recognition or autonomous gameplay.
