# Local card snapshots

Source: https://api.gwent.one/ (gwent.one). Required version: 14.10.0; languages: en and cn; response: JSON. Obtain snapshots through the provider's documented API access or export process and place them at:

- `data/cards/14.10.0/en/cards.json`
- `data/cards/14.10.0/cn/cards.json`

The source may require an API key. Keep it in your own environment; do not commit credentials or URLs containing keys. A credential-free download was attempted on 2026-10-31 and did not return JSON, so this repository does not advertise an unverified automatic download command. No third-party snapshot is distributed here.

The locally tested snapshots contain 1,381 records each, including leaders and non-ownable cards. Tests require these files and fail clearly if absent; after local import run `go test ./...`. Use `go run ./cmd/carddb -query 152405` to inspect the imported catalog. Version metadata is not proof of the current client's rules.

All game text and artwork rights remain with their respective authors/CD PROJEKT RED. Source attribution is not a redistribution license. Confirm provider terms and relevant rights before redistributing data. Images are not downloaded by this project.
