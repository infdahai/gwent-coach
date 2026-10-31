# Card Lookup and Image Candidates

`cmd/cardlookup` imports the locally supplied card snapshots into an embedded database, creates an art-template index, and returns card/effect records or image candidates. It has no game input, remote control, network download, or automatic action capability. All generated data belongs under the ignored `runs/` directory; source snapshots, templates, and account images must not be committed.

## Storage and executable support

The database is bbolt v1.4.3, a transactional embedded B+tree with no server or CGO requirement. Keys are `14.10.0/native-v1` → `cards` → decimal card ID; localized records live together under that ID. The import retains every supplied card's parsed identity, art ID, name, category, ability text, and attributes. Extra upstream JSON fields remain in the original local snapshots, not in the database. The current English/Chinese snapshots produce 1,381 IDs, each with both languages.

The existing `native-v1` registry validates the English text and statistics. Exactly six records receive handler identifiers such as `native-v1/damage`; all other records retain their printed text with `supported: false` and an empty handler. A handler string is metadata referring to the existing native implementation, not code interpreted from text. Execution still goes through `internal/rules`, including its restrictions. An unsupported card is never treated as vanilla.

Import rejects duplicate JSON keys, repeated card IDs within a language, duplicate languages, inconsistent localized identities/statistics, and unsupported data/rules versions. English is mandatory for registry validation. Import is transactional and refuses to overwrite an existing version bucket. Use a new database path to rebuild. Opening validates stored version metadata and each handler against the native registry, then closes bbolt and retains an immutable in-memory map. Returned cards are values; callers cannot mutate the index.

This chooses a real database over a JSON-only map to provide indexed persistence, transactions, and a path to independent record updates. SQLite would provide SQL and richer queries, but would require CGO or a larger pure-Go dependency stack for functionality not currently needed. bbolt adds one direct module, reuses the existing `x/sys` runtime dependency, and adds dependency-test checksums through `go mod tidy`. The tradeoff is a full validation/load at startup and memory proportional to catalog size; steady-state lookup does not touch disk. There is no hot reload, name search, multi-writer service, or editable-effect API in this package. The original `cmd/carddb` remains available for name searches.

## Commands

Set up local snapshots as described in [card data setup](../data/cards/README.md), then:

```sh
mkdir -p runs

go run ./cmd/cardlookup -mode import -db runs/cards.db \
  -sources data/cards/14.10.0/en/cards.json,data/cards/14.10.0/cn/cards.json

go run ./cmd/cardlookup -mode card -db runs/cards.db -id 113301 -language en
go run ./cmd/cardlookup -mode card -db runs/cards.db -id 112101 -language cn
```

The first query reports `native-v1/damage` and supported. The second reports Ciri's printed ability and unsupported. An absent ID/language reports `status: unknown`. Both data and rules versions are explicit in the API and output; CLI defaults pin the currently supported pair. A different version is rejected.

A local sample manifest has this shape. The example describes the schema only; it does not supply an image or assert its identity:

```json
{
  "data_version": "14.10.0",
  "rules_version": "native-v1",
  "samples": [
    {
      "card_id": 113301,
      "image": "preview.png",
      "roi": {"x": 120, "y": 100, "width": 180, "height": 160}
    }
  ]
}
```

Paths are relative to the manifest unless absolute. `card_id` is an integer. Each ROI is an explicit art rectangle in original image coordinates, not a whole-card or screen bounding box. Multiple templates for an ID are allowed. The sample collector is responsible for verifying the ID, choosing comparable art crops, and keeping any screen/input coordinate transformation outside this package.

```sh
go run ./cmd/cardlookup -mode templates -db runs/cards.db \
  -manifest runs/samples.json -templates runs/templates.json

# Zero distance and gap one are demonstration settings, NOT calibrated defaults.
go run ./cmd/cardlookup -mode image -db runs/cards.db \
  -templates runs/templates.json -image runs/preview.png \
  -roi 120,100,180,160 -max-distance 0 -min-gap 1

# Restrict to verified deck identities only when all have templates.
go run ./cmd/cardlookup -mode image -db runs/cards.db \
  -templates runs/templates.json -image runs/preview.png \
  -roi 120,100,180,160 -max-distance 0 -min-gap 1 -deck 113301,113310
```

Template creation refuses to overwrite an existing file. Images support PNG/JPEG, with a CLI decoding limit of 16 million pixels. Template files include data/rules and feature versions; they contain only IDs and 72 grayscale samples per template, but remain local data derived from artwork.

Image queries require both thresholds. Output is always `candidate` or `unknown`, with `calibration: unvalidated`; no flag can turn a candidate into a trusted identity. Bad input returns a nonzero exit status. Ordinary unknown recognition returns JSON and exit status zero. Output reports decode, normalization, match, and database startup timing separately. CLI timings include timer overhead and cold-process effects; use benchmarks for repeated runtime operations.

## Matching contract and integration

`internal/cardmatch.Normalize(image.Image, ROI)` area-averages the art ROI to a 9×8 grayscale thumbnail. Matching compares its 64-bit horizontal difference hash with prebuilt hashes using Hamming distance. It rejects invalid/small/out-of-bounds ROIs, transparency, and thumbnails whose grayscale range is below 8. That range check is an input-quality filter, not an accuracy calibration.

The best template for each distinct card ID contributes one score. Accepting a candidate requires both the caller's maximum absolute distance and minimum best-to-runner-up gap. Shared art across distinct IDs therefore rejects as ambiguous. Multiple templates or deck copies of one ID do not count as separate runner-ups. With only one candidate ID, there is no runner-up and only the absolute cutoff applies. `runner_distance: -1` denotes that case.

- `cardindex.Import(path, Version{Data, Rules}, sourcePaths)` creates persisted records.
- `cardindex.Open(path, version)` returns an immutable index. Reuse it for the training session.
- `index.Lookup(id, language)` returns `(cards.Card, handler string, supported bool, found bool)`.
- `cardmatch.New(File{Version, FeatureVersion, Templates}, database)` validates template versions and IDs and precomputes hashes.
- `matcher.Match(feature, deckIDs, Thresholds{MaxDistance, MinGap})` returns diagnostic candidate/unknown output. A nil deck means all templates; an empty deck, or any deck ID without a template, is an error.

This is a small linear hash scan over the chosen IDs with indexed per-ID template access. A 25-card deck does not require searching every template in the database. Color/thumbnail tie-breaking is deliberately omitted: a collision produces unknown instead of an unsupported confidence claim. The caller must preserve uncertainty, especially for unknown opponent cards, stale deck lists, animated/premium art, obscured or tilted cards, and art shared by different IDs.

## Verification and measured cost

On 2026-10-31, Go 1.27.3, macOS darwin/arm64, Apple M4 Pro, a single benchmark run measured:

| Prebuilt operation | ns/op | B/op | allocs/op |
| --- | ---: | ---: | ---: |
| Card lookup, English ID | 19.14 | 0 | 0 |
| Normalize synthetic 180×160 grayscale ROI | 77,082 | 0 | 0 |
| Match prebuilt synthetic hashes, 25 supplied IDs | 475.4 | 208 | 1 |
| Match prebuilt synthetic hashes, 1,381 supplied IDs | 20,325 | 12,288 | 1 |

Reproduce with:

```sh
go test ./internal/cardindex ./internal/cardmatch -run '^$' -bench . -benchmem -count=1
go test ./...
go vet ./...
go test -race ./internal/cardindex ./internal/cardmatch
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build ./...
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go vet ./...
```

The database benchmark excludes loading/import. The matcher benchmark excludes template construction, image decode, and normalization; normalization is measured independently. These synthetic tests verify algorithm contracts, including collisions, repeated art IDs, deck filtering, unknowns, version rejection, and persistence. They establish no real-game recognition rate. The actual CLI was also exercised with a bilingual import, supported/unsupported lookups, synthetic PNG template creation, a candidate query, and a deck-excluded unknown query. One import plus validation took 79.23 ms and database opens around 6.6–7.0 ms in that demo; these are single observations, not stable throughput measurements.

A local plumbing check also imported three real Tactical Advantage collection previews (ID 202140, 83×110 art ROIs) supplied by the navigation worker. Querying a frame already included among those templates returned candidate 202140 at distance zero with no distinct-ID runner-up. These selected/tinted frames and the self-match do not establish held-out or gameplay accuracy. All images and derived templates stayed local and ignored.

Real-image collection is a separate activity. Before trusting identities, obtain independently labeled template and held-out query crops across the intended game phases, scale, animation, and capture conditions; measure false candidates and unknowns, including out-of-deck negatives and shared art, and choose thresholds against that evidence. Reusing the template image as the query is only a plumbing check. Windows was cross-built and vetted; these timings are not Windows runtime measurements.
