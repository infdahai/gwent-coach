# Local card snapshots

Source: https://api.gwent.one/ (gwent.one). Required version: 14.10.0; languages: en and cn; response: JSON. Obtain snapshots through the provider's documented API access or export process and place them at:

- `data/cards/14.10.0/en/cards.json`
- `data/cards/14.10.0/cn/cards.json`

The source may require an API key. Keep it in your own environment; do not commit credentials or URLs containing keys. A credential-free download was attempted on 2026-10-31 and did not return JSON, so this repository does not advertise an unverified automatic download command. No third-party snapshot is distributed here.

The locally tested snapshots contain 1,381 records each, including leaders and non-ownable cards. Tests require these files and fail clearly if absent; after local import run `go test ./...`. Use `go run ./cmd/carddb -query 152405` to inspect the imported catalog. Version metadata is not proof of the current client's rules.

Local verification fingerprints (SHA-256 of the exact imported files, including whitespace):

- en: `e95c293ff7113e98f548ea530fcedffecb54abba54465ef034d8b2b4dcfca42c`
- cn: `23a7a260faae50da307bfc7ccfcf1b47945f5a4f76ba90e76a0c5122c0f6e28b`

These identify the tested local exports; formatting changes may produce a different hash. Inventory and effect-text tests provide semantic validation.

All game text and artwork rights remain with their respective authors/CD PROJEKT RED. Source attribution is not a redistribution license. Confirm provider terms and relevant rights before redistributing data. Images are not downloaded by this project.
