# Mac offline training worker

The worker processes local sealed match directories independently of Windows game execution. It never sends inputs or promotes a policy. Run one scan, or use `-watch 30s` for repeated scans:

```sh
go run ./cmd/trainworker -inbox "$HOME/gwent-inbox" -store "$HOME/gwent-worker"
go run ./cmd/trainworker -inbox "$HOME/gwent-inbox" -store "$HOME/gwent-worker" -watch 30s
```

Each inbox child directory must be named for its match ID and contain `events.jsonl`, the derived `training.jsonl`, and the zero-byte `COMPLETE` marker published after both files. `go run ./cmd/matchlog import -source 'C:\coach\bearly-runs' -dest "$HOME/gwent-inbox" -watch 30s` imports sealed training directories over the existing SSH transport. A marker is a publication signal, not a cryptographic receipt; the current producer has no source digest to authenticate transfer. The worker compares the export to confirmed events, validates the action protocol and calculates a SHA-256 for retained data; it cannot detect identical corruption of source and transfer or establish authenticity.

Reports identify incomplete, rejected, imported and duplicate matches. A conflicting match ID is rejected. Private retained logs are atomically written under `store/matches`; scans reconstruct state from them after restart. Run one worker per store. The worker does not delete input. A complete match without confirmed actions is valid but contributes no action samples. Unfinished actions and missing receipts are rejected. Unknown outcomes are counted separately, never as wins or losses.

No candidate is emitted: the producer does not yet bind policy, rules and catalog provenance, and the outcome observation has no independently validated evidence contract. All logs remain in descriptive quarantine, including those with a session ID; outcomes count as unknown. Existing `candidate.json` files are moved to `quarantined-candidate-*.json`. `report.json` contains counts, action and loop metrics, and a fingerprint over sorted match IDs and raw event digests. Ordered legal candidate sets are not recorded, so held-out ranking agreement is unavailable. Add and verify shared provenance and result evidence contracts before enabling candidate computation or reporting known outcomes.
