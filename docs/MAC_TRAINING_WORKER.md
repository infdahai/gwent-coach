# Mac offline training worker

The worker processes local sealed match directories independently of Windows game execution. It never sends inputs or promotes a policy. Run one scan, or use `-watch 30s` for repeated scans:

```sh
go run ./cmd/trainworker -inbox "$HOME/gwent-inbox" -store "$HOME/gwent-worker"
go run ./cmd/trainworker -inbox "$HOME/gwent-inbox" -store "$HOME/gwent-worker" -watch 30s
```

Each inbox child directory must be named for its match ID and contain `events.jsonl`, the derived `training.jsonl`, and the zero-byte `COMPLETE` marker published after both files. `go run ./cmd/matchlog import -source 'C:\coach\bearly-runs' -dest "$HOME/gwent-inbox" -watch 30s` imports sealed training directories over the existing SSH transport. A marker is a publication signal, not a cryptographic receipt; the current producer has no source digest to authenticate transfer. The worker compares the export to confirmed events, validates the action protocol and calculates a SHA-256 for retained data; it cannot detect identical corruption of source and transfer or establish authenticity.

Reports identify incomplete, rejected, imported and duplicate matches. A conflicting match ID is rejected. Private retained logs are atomically written under `store/matches`; scans reconstruct state from them after restart. Run one worker per store. The worker does not delete input. A complete match without confirmed actions is valid but contributes no action samples. Unfinished actions and missing receipts are rejected. Unknown outcomes are counted separately, never as wins or losses.

`candidate.json` is a versioned strategy envelope containing an `action-ranker-v1` payload with zero weights. It preserves baseline candidate order and makes no claim of improvement; confirmed behavior alone cannot justify outcome-based weights. `report.json` separately contains descriptive offline statistics, training-set action frequencies, held-out counts, and a data fingerprint. The split hashes session IDs; legacy logs without a session ID are reported separately and excluded from both partitions. Ordered legal candidate sets are not recorded, so held-out ranking agreement is unavailable and reported as `null`, and candidate status is `insufficient_data`. Empty or small partitions do not establish effectiveness. `data_fingerprint` covers sorted match IDs and retained event digests. Rules, catalog and policy versions are explicit flags and must match the actual producer; the present event protocol does not bind those versions to each match. Integrate version metadata and source digests into the producer contract before relying on these fields as provenance.
