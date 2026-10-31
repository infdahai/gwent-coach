# Gwent Coach

Go observation prototype and versioned card catalog for Gwent research. This project is not an autonomous player. Windows observation/input tooling requires a local interactive game session; no game account, remote host or credentials are configured by this repository.

Card snapshots are **not distributed**. See [card data setup](data/cards/README.md) before running catalog commands or integration tests. Game text and artwork rights remain with their owners; public API availability does not establish redistribution permission.

```sh
go test ./...
go run ./cmd/carddb -query 152405
```

No screenshots, run archives or local machine notes are included in the public repository.
