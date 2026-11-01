# Windows read-only collection demo

The collector captures a bounded series of frames from the foreground Gwent window and records experimental numeric candidates. It does not send input or play matches. Run it in the same unlocked interactive Windows session as the restored, foreground game. The existing capture checks reject other sessions, locked desktops and foreground changes.

## Build and configure

From the repository on Windows, build with Go:

```powershell
New-Item -ItemType Directory -Force .\bin | Out-Null
go build -o .\bin\coach.exe ./cmd/coach
```

Save the following as `collector.json`. This example was calibrated against a small set of 1920×1080 frames and contains templates only for 0 and 1. Its coordinates and thresholds may not match another window, resolution, UI or game state. Area-based matching changes the distance scale: calibrations created for the former point sampler must be recalibrated and declare `"Method": "area-v1"`. The collector rejects configurations without this marker. The example has only four labeled historical frames and cannot reliably reject all other digits.

```json
{
  "Digits": {
    "opponent_score": {
      "ROI": {"Min": {"X": 1815, "Y": 345}, "Max": {"X": 1875, "Y": 400}},
      "Threshold": 140, "CandidateOnly": true, "Method": "area-v1",
      "Width": 5, "Height": 7, "MaxDigits": 2, "MaxDistance": 9, "MinGap": 4,
      "Templates": {"0": ["01110110111100111001110011101101110"], "1": ["00111000110001100011000110001100011", "01111001110011100111001110011100000"]}
    },
    "own_score": {
      "ROI": {"Min": {"X": 1815, "Y": 675}, "Max": {"X": 1875, "Y": 730}},
      "Threshold": 140, "CandidateOnly": true, "Method": "area-v1",
      "Width": 5, "Height": 7, "MaxDigits": 2, "MaxDistance": 9, "MinGap": 4,
      "Templates": {"0": ["01110110111100111001110011101101110"], "1": ["00111000110001100011000110001100011", "01111001110011100111001110011100000"]}
    }
  }
}
```

## Run

With the game foreground in the interactive session, run the executable from that session. The stop-file argument is required; an existing file at that path pauses capture. Choose a path that does not currently exist.

```powershell
.\bin\coach.exe -out .\runs -collect-readonly .\collector.json -stop-file .\PAUSE -samples 10 -interval 1s
```

The collector accepts 1–1,200 frames per invocation and intervals of at least 100 ms. For example, `-samples 1200 -interval 1s` targets roughly 20 minutes; capture and processing time can extend the duration. It stops on capture or recognition errors. `runs\coach.log` records errors; each invocation creates `runs\readonly-collection-*\recognition.jsonl` and per-frame directories containing a timestamped capture report and JPEG. The `reference` field points to the report, which identifies the screenshot. Runs accumulate across invocations; remove old local runs when no longer needed. Screenshots and reports can contain private information.

On the Mac, copy a run's `recognition.jsonl` locally and summarize it with `go run ./cmd/collection-analysis /path/to/recognition.jsonl`. The JSON output counts frames, candidate values, unknown frames, and reported consecutive candidate agreements for each scalar. It retains the `unverified_candidate_not_for_actions` trust marker; agreement and counts do not validate recognition. The command reads only the JSONL, not the referenced screenshots, and does not train a model or report match results.

Each JSONL line has `captured_at`, `reference`, `scalars`, `unknown`, and the trust marker `unverified_candidate_not_for_actions`. `agreed`, when present, records agreement between consecutive candidate readings; it does not establish correctness. Menus, occlusion, and similar-looking glyphs can yield false values. These candidates are rejected by the gameplay evidence reader and are not connected to `Facts.Totals` or action selection. Validate frames manually before using them as research data; the demo does not provide reliable score recognition or autonomous gameplay.

## Offline hand annotation and evaluation

Keep screenshots, annotations, reports, and outputs in a private local directory outside the repository. Open a saved JPEG or PNG in an image viewer that shows pixel coordinates. For each visible hand card, record the **art region** (the same region used by the calibrated scanner, not the full card outline) in native image pixels. Use x and y for its top-left corner and width and height for its extent. Record the catalog card ID if known, or 0 if uncertain. Include every visible card, even uncertain ones; use an empty `cards` array for a deliberately reviewed empty hand. Label each frame independently, including both frames of a scan if evaluating both. Do not copy predictions into labels without independent review.

Save an annotation as JSON, substituting your own paths, report reference, image dimensions and observations:

```json
{
  "report": "frame-1-report.json",
  "image": "/private/run/frame-1.jpg",
  "width": 1920,
  "height": 1080,
  "cards": [
    {"art": {"x": 600, "y": 850, "width": 90, "height": 110}, "card_id": 152405},
    {"art": {"x": 700, "y": 850, "width": 90, "height": 110}, "card_id": 0}
  ]
}
```

`report` is an exact identifier, not automatically resolved from the image; make sure it identifies the actual annotated screenshot. A hand-scan JSON line produced on Windows can be saved as a single JSON file. Alternatively, run the existing scanner offline against two saved frames using the same `HandScanConfig`, card database and template file described in [card lookup](CARD_LOOKUP.md). Database and template paths are relative to the config file. The two report identifiers are supplied by you and must correspond to the actual images:

```sh
go run ./cmd/hand-scan-offline /private/hand-config.json /private/frame-1.jpg frame-1-report.json /private/frame-2.jpg frame-2-report.json > /private/scan.json
go run ./cmd/hand-eval /private/annotation.json /private/scan.json
```

A localization experiment can instead provide a single-frame JSON object such as `{"report":"early","regions":[{"x":500,"y":940,"width":40,"height":100}]}` and run `go run ./cmd/hand-eval --regions /private/annotation.json /private/regions.json`. Use an empty `regions` array for no detections. The report identifier must match the annotation; regions are evaluated as unknown identities. This format accepts art rectangles, not card edges or full-card outlines. Keep detection and annotation independent and record the method and calibration frames separately.

The evaluator checks image dimensions and requires the annotation report to match either scan reference. It pairs art rectangles one-to-one at intersection over union >= 0.5, maximizing matched count and then total overlap. It reports unmatched labels and regions, and identity agreements, disagreements, unknown predictions and unknown labels for matched pairs. The scanner uses configured regions; these counts do not measure an automatic card locator. A scan candidate reflects agreement across both frames: check that the hand remains unchanged, and annotate and evaluate both frames if using both as evidence. Paths and reference strings are not proof of image provenance. Labels and predictions remain unverified research data, not game state or action authority. Report sample selection, unknowns and limitations; do not claim real-world accuracy from synthetic tests or template self-matches. Use independently labeled held-out images for meaningful evaluation.
