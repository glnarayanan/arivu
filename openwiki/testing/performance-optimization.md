# Batched-read optimization report

Measured on 2026-09-28 in an x64 Linux orb with Go 1.25.13 and the bundled
SQLite driver. This is a hand-written measurement record, not a generated wiki
page.

## Results and limits

Median elapsed time across three benchmark samples:

| Operation | Fixture | Before | After | Less time |
| --- | --- | ---: | ---: | ---: |
| Search rebuild | 1,000 bookmarks and linked notes | 825 ms | 49.9 ms | 93.9% |
| Search rebuild | 10,000 bookmarks and linked notes | 54.73 s | 0.521 s | 99.0% |
| Full export | 1,000 bookmarks and linked notes | 470 ms | 98.6 ms | 79.0% |
| Full export | 10,000 bookmarks and linked notes | 28.66 s | 2.615 s | 90.9% |
| Capture-status reads | 1,000 bookmark IDs | 14.0 ms | 7.89 ms | 43.5% |
| Graph edge builder | 10,000 bookmarks, 80 selected nodes, 500 edges | 14.3 ms | 1.27 ms | 91.1% |

These are local operation benchmarks, not production HTTP latency or an
app-wide speed claim. Fixtures use short text and three-dimensional graph
vectors; results vary with data, hardware, and load. Search/export samples use
one operation per sample; capture/graph samples use ten. Setup runs outside the
timer. Search/export fixtures give each bookmark a note, summary, tag,
annotation, and link. The graph benchmark checks the returned edge count.

Production source shrank by only **12 physical lines**, from 36,512 to 36,500
(0.03%). The count includes tracked Go, JavaScript, CSS, HTML, and SQL, but
excludes tests and fixtures. The requested 20% cut was **not achieved**.
Removing tests does not count toward production-code reduction. No frontend
rewrite, dependency, feature removal, schema migration, or security exception
was needed for these gains.

## Changes and retained contracts

- Library reads capture states for a page in one owner-scoped query. Latest
  attempts, staged/deleted artifacts, and status labels retain their meaning.
- Search builds its replacement from one query. Materialized note/link groups
  avoid repeated scans; ordered groups retain the newest 100 notes/annotations
  and 200 links. Prepared inserts replace both regular and FTS indexes in the
  existing transaction, behind the existing search lock.
- Export reads each detail relation once and shares row decoders with detail
  views. It retains ordering, per-bookmark limits, portable evidence HTML,
  null versus empty lists, and all-or-nothing evidence reads per bookmark.
- Insights shares one ordered concept/source read across three detectors and
  loads hidden-feedback targets once. Family filtering still follows ranking
  and diversification.
- Graph fetches embeddings only for selected bookmark nodes. Graph and
  duplicate detection reuse squared norms without changing thresholds,
  ordering, or the cosine denominator.

Incremental indexing and broad frontend consolidation remain deferred. They
need separate invalidation and browser-compatibility work; these measurements
do not justify claiming those changes are risk-free.

## Reproduce

Run the same benchmark fixtures against the baseline implementation and the
changed implementation. The baseline was
[`428a94f`](https://github.com/glnarayanan/arivu/commit/428a94fcfcd714b950e59182035697e37ecc9f0c).
For the baseline, copy the projection benchmark file
and just the graph benchmark (not tests of new helpers) into a separate
checkout. The capture benchmark compares the retained singleton method with
the batched method in one build.

```sh
GOCACHE=/tmp/arivu-build-cache go test ./internal/bookmarks -run '^$' \
  -bench 'Benchmark(SearchRebuild|FullExport)' -benchmem -benchtime=1x -count=3
GOCACHE=/tmp/arivu-build-cache go test ./internal/bookmarks -run '^$' \
  -bench 'Benchmark(CaptureStatusReads1000|GraphV2Edges10KCorpus)' \
  -benchmem -benchtime=10x -count=3
```

Fixtures live in `internal/bookmarks/projection_performance_test.go`,
`batch_reads_test.go`, and `graph_insights_performance_test.go`.

## Verification

- `go test ./...` and `go test -tags sqlite_fts5 ./...`: passed, using the
  writable cache path above. Opt-in external integrations did not run.
- `node --test internal/app/webtest/*.test.mjs extension/*.test.mjs`: 33 passed.
  `npm test --prefix capture`: 10 passed. Frontend JavaScript syntax checks
  passed. `gofmt -l` and `git diff --check` returned no findings.
- Search/export compatibility tests passed on both implementations, including
  text composition, incoming/self-links, 100/200-item boundaries, empty JSON
  shapes, evidence resolution, and a damaged evidence row.
- Browser checks on a disposable local database covered sign-in, Library
  pagination through 65 bookmarks, note creation/editing, fresh search results,
  populated Graph, and dismissing an insight. Graph rendered 48 nodes and 160
  relationships. No UI source changed.
- Eleven authenticated API responses matched the baseline: Library pages and
  filters, Graph and focused Graph, Insights and a family filter, bookmark
  details, export, old/new search terms, and duplicate detection. Only the
  export timestamp was excluded. Both databases ran startup migrations before
  comparison.
- Full JSON export/import into a second disposable account retained 65
  bookmarks, 65 evidence records, the edited note, and a bookmark annotation.
- Final Go statement coverage: 6,327 / 13,836 = **45.73%**, versus 47.13% before
  test pruning. Removed 58 of 292 original top-level tests; seven targeted
  tests bring the final count to 241. Net test-count reduction is 17.5%.

Two existing checks remain red. `go vet ./...` reports duplicate `xml:"href,attr"`
tags for `Href` and `Rel` in `internal/bookmarks/release_c.go:47`.
`go test -race ./...` reports a race in `TestSemanticKnowledgeGraphParity`:
the test replaces `App.bookmarks` while a worker reads it. The same race
reproduced on the unchanged baseline; the bookmarks package passed the race
run. Neither unrelated issue was changed. Ponytail Audit and CE Code Review
were unavailable in this environment.
