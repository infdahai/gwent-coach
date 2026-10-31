# Asynchronous match logs

This is a proposed v1 integration contract, agreed with the Windows runtime worker; the preliminary runtime writer follows these field types but is not yet wired into gameplay. Existing Bearly sampling reports and navigation events use different formats and are not match logs. No gameplay or training is performed by this command.

A producer writes one directory per unique match ID. `events.jsonl` is UTF-8 JSON Lines, with a newline after every record. Every event has `schema_version: 1`, the same string `match_id` matching the directory name, contiguous integer `sequence` beginning at 1, RFC3339 `utc`, nonnegative monotonically nondecreasing `elapsed_ns` relative to process start, and string `event_type`. Measured `timing_ms` is an object of nonnegative numeric samples, e.g. `observation`, `decision`, and `action`; null values are unknown. A non-null `error` indicates a failure; a string error containing `timeout` is counted as a timeout. Explicit boolean `manual_intervention` marks an intervention. These counts are per event, not necessarily distinct incidents. Do not record opponent private information or infer hidden hands.

The final event is `match_completed` or `match_incomplete`, with `result` set to `win`, `loss`, `draw`, `unknown`, or null. Unknown remains unknown even for a completed match. After writing and closing the terminal record, the producer creates a zero-byte `COMPLETE` file and never changes the published log. A crash before publication leaves an unmarked directory, which the importer reports but does not import. Extra fields may carry public observations, intents and confirmation; the analyzer ignores them.

Use an existing SSH configuration and provide the Windows source directory explicitly. This reads remote files and does not alter Windows credentials or game control. Destination files contain potentially sensitive local data; keep them private and outside the repository.

```sh
go run ./cmd/matchlog import -source 'C:\coach\bearly-runs' -dest "$HOME/gwent-match-logs" -watch 30s
# Ctrl-C stops the watcher gracefully.
go run ./cmd/matchlog analyze "$HOME"/gwent-match-logs/*.jsonl
```

Watch mode imports and prints a per-match JSON analysis when first seen; subsequent polls report already imported logs. Each poll rereads and transfers every published log to detect changes; large archives consume network bandwidth and delay discovery of later matches. Responses are processed one log at a time, with a 16 MiB remote file limit. `-progress-timeout` sets the maximum time without SSH output (default `2m`); a stalled transfer is cancelled and watch mode retries on its next poll. This timer also applies while processing a received record, so increase it for slow local storage. A single import is obtained by omitting `-watch`. Files are validated before import, created exclusively with private permissions, and compared byte-for-byte on repeat; conflicts and malformed published logs are errors. A failed local write may leave a partial destination, requiring manual inspection; it is never overwritten. Analysis emits one JSON object per valid distinct match, including raw timing samples and counts, terminal status and outcome. It does not calculate percentiles from small samples. It exits nonzero on malformed files, gaps, duplicate IDs, or conflicting IDs, while reporting valid files separately. No files matching a shell glob should be treated as zero successful matches.

Improvement hypotheses should be grounded in collected samples: for example, investigate repeated observation timeouts, slow decision samples, or frequent manual interventions before changing the policy. These are hypotheses, not evidence of training or improved play.
