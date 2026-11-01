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

For a read-only remote pull on the Mac, use an existing SSH host configuration and a separate private local inbox:

```sh
mkdir -p "$HOME/gwent-collection-inbox"
chmod 700 "$HOME/gwent-collection-inbox"
go run ./cmd/collection-analysis pull -host WINDOWS_SSH_HOST -source 'D:\path\to\runs' -dest "$HOME/gwent-collection-inbox" -watch 30s
```

Omit `-watch` for one pull. Each poll reads Windows collection JSONL files using PowerShell with shared read/write access, snapshots their length, validates and incrementally imports complete lines, and prints unverified per-run summaries. Errors are reported and watch mode retries on the next poll; one-shot mode exits with an error. Each pull has a two-minute timeout (adjust with `-timeout`) and a 16 MiB limit per run. Runs are processed one at a time with bounded memory; failed or oversized runs are reported without blocking other runs. Each poll rereads all discovered JSONL files, so large archives increase transfer time and may require a longer timeout. Successful runs remain imported if another run or the transport fails. It reads only JSONL, not screenshots or capture reports; copy those separately for manual labeling. SSH and filesystem permissions must already be configured. No match results or gameplay actions are generated.

On the Mac, a locally mounted or copied collection root can be scanned repeatedly with `go run ./cmd/collection-analysis import /path/to/collection-root /private/collection-inbox`. Create the private inbox first and keep it separate from both the source and the sealed-match training inbox. Each scan discovers immediate `readonly-collection-*` directories, validates complete JSONL lines, and atomically updates one private JSONL snapshot per run. An incomplete final line is deferred until the next scan; source truncation or changes to already imported bytes cause an error. Schedule repeated scans externally. The importer copies only JSONL records, reads the entire JSONL each time, and does not transfer screenshots or reports, connect to Windows, or verify source identity. macOS and Linux use an operating-system lock that is automatically released if the importer exits; keep the persistent `.collection-import.lock` file in place. An old `.collection-import-lock` directory from earlier versions is ignored.

Alternatively, copy a run's `recognition.jsonl` locally and summarize it with `go run ./cmd/collection-analysis /path/to/recognition.jsonl`. The JSON output counts frames, candidate values, unknown frames, and reported consecutive candidate agreements for each scalar. It retains the `unverified_candidate_not_for_actions` trust marker; agreement and counts do not validate recognition. The command reads only the JSONL, not the referenced screenshots, and does not train a model or report match results.

To evaluate numeric candidates, independently inspect saved screenshots and create a private JSON array of labels. Use the exact `reference` string from each JSONL record, including the original Windows path, and include only scalars whose values you can determine:

```json
[{"reference":"C:\\runs\\frame1\\report.json","scalars":{"own_score":12,"opponent_score":8}}]
```

Run `go run ./cmd/collection-analysis /path/to/recognition.jsonl /path/to/annotations.json`. For each labelled scalar the `evaluation` object counts correct candidates, incorrect candidates and unknown predictions. Labels must reference unique records and scalar names present in those records; unmatched or duplicate references are errors. Unlabelled frames and scalars remain outside the evaluation. Labels and reference strings are not independently verified, and these per-frame counts are not match outcomes or a representative accuracy estimate.

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
