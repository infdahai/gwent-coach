# Gwent Coach

Go observation prototype and versioned card catalog for Gwent research. This project is not an autonomous player. Windows observation/input tooling requires a local interactive game session; no game account, remote host or credentials are configured by this repository.

Card snapshots are **not distributed**. See [card data setup](data/cards/README.md) before running catalog commands or integration tests. Game text and artwork rights remain with their owners; public API availability does not establish redistribution permission.

After importing the local snapshots:

```sh
go test ./...
go vet ./...
GOOS=windows GOARCH=amd64 go build ./...
GOOS=windows GOARCH=amd64 go vet ./...
go run ./cmd/carddb -query 152405
go run ./cmd/rules -mode coverage > /tmp/gwent-coverage.json
go run ./cmd/rules -mode demo > /tmp/gwent-demo.json
go run ./cmd/rules -mode replay -input /tmp/gwent-demo.json
```

The pure Go `native-v1` offline engine implements six pinned card abilities, armor/damage/boost/death, atomic actions, Pass, resource retention, match settlement, deterministic JSON replay and a prediction/confirmation adapter. All 1,381 local catalog entries appear in the coverage report; 1,375 are explicitly unsupported and rejected by the simulator.

See [architecture, evidence and next coverage steps](docs/NATIVE_RULES.md). Round ties and transition constraints are explicit experimental profile choices, not verified seasonal rules. The simulator holds full information, while policy views hide opponent hands and deck order. Live partial-state tracking, OCR, full-card effects, search, self-play tuning and reinforcement learning are not implemented. Tests intentionally fail with a missing-file error until snapshots are imported; no network access occurs during tests.

See [offline decision prototype](docs/DECISIONS.md) for the public-view decision API, demo, and current one-move limitations.

See [persistent card/effect lookup and image candidates](docs/CARD_LOOKUP.md) for the local database, explicit art-ROI matcher, CLI commands, integration APIs, and measured costs. Image results remain unvalidated candidates.

No screenshots, run archives or local machine notes are included in the public repository.
