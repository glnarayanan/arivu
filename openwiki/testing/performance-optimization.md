# Batched-read optimization report

Measured on 2026-09-28 in an x64 Linux orb with Go 1.25.13 and the bundled
SQLite driver. This is a hand-written measurement record, not a generated wiki
page.

The first-pass results below cover the batched-read foundation. The
[request-latency follow-up](#request-latency-follow-up) records the later
Notes, search-feedback, and Graph changes separately; the original line and
coverage counts are not totals for both passes.

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

## Request-latency follow-up

This pass compares against the
[batched-read foundation](https://github.com/glnarayanan/arivu/commit/0f9e8343908b241de98559081f893978ee57b7fc).
It preserves features and UI. It does not pursue a coverage or line-count target.

- Notes loads states, tasks, reminders, and each link direction for selected
  notes in batches. SQL window ranks retain each note's limits. Duplicate
  list rows remain separate, and title lookups remain owner-scoped and bounded.
- Search joins selected result keys to the existing feedback primary key.
  An initial OR-predicate query was slower on populated data and was rejected.
  Candidate selection, scores, and stable ranking ties remain unchanged.
- Graph applies selected-endpoint predicates to relationship queries and skips
  later families when the edge budget is full. It retains source-type aliases,
  provenance checks, hidden-edge filtering, and family precedence.

### Measurements

Operation medians across three samples; setup is outside the timer:

| Operation | Before | After |
| --- | ---: | ---: |
| Decorate 200 notes, no related rows | 11.98 ms | 0.80 ms |
| Decorate 200 notes, 100 tasks each | 1,113.59 ms | 76.40 ms |
| Feedback for 20 results, 1,000 feedback rows | 0.265 ms | 0.108 ms |
| Feedback for 50 results, 1,000 feedback rows | 0.609 ms | 0.245 ms |
| Graph edges, two selected nodes in 5,000 bookmark-note pairs | 6.33 ms | 0.20 ms |

Authenticated loopback HTTP checks used two identical disposable databases:
5,000 notes and bookmarks, 5,000 bookmark-note links, 2,000 tasks, 200 reminders,
200 explicit links, and 1,000 feedback rows. Both binaries used the same Go
toolchain and default non-FTS build. Response bodies matched before timing.
Each endpoint had 25 samples after warmup; p95 is the 24th sorted sample.
These small local samples are not a production latency guarantee.

| Endpoint | Median before → after | p95 before → after |
| --- | ---: | ---: |
| Notes | 168.94 → 75.27 ms | 198.62 → 91.06 ms |
| Search items | 1.57 → 1.95 ms | 1.93 → 3.33 ms |
| Recent Graph, 48 nodes / 160 edges | 271.00 → 276.39 ms | 282.24 → 299.35 ms |
| Focused Graph, depth 2 | 13.22 → 3.09 ms | 15.80 → 3.58 ms |

Ten concurrent rounds of Notes, Search, and recent Graph completed in a median
471.76 ms before and 313.66 ms after. Notes and focused Graph improved, but the
full Search and recent Graph endpoints did not show a gain in this run. Do not
substitute the microbenchmark gains for endpoint latency. Recent Graph still
does substantial node-selection work.

A note edit took 154 ms in one check and appeared in search immediately.
Incremental indexing and Insights caching remain deferred: the measurements
do not justify expanding this pass into new invalidation machinery, especially
ahead of the planned feature-simplification pass.

### Checks and reproduction

Added three focused tests: Notes response parity with singleton reads at the
100-task, 50-reminder, and 100-link boundaries; search scores and stable ties
with mixed item IDs, owners, and surfaces; and exact Graph edges with dismissed
edges, unrelated rows, source aliases, and competing family limits. The Graph
fixture also passed against the foundation code. No timing assertion or
coverage gate was added.

```sh
GOCACHE=/tmp/arivu-build-cache go test ./internal/bookmarks -run '^$' \
  -bench 'BenchmarkDecorateNotes200|BenchmarkGraphV2EdgesRelationship' \
  -benchmem -benchtime=3x -count=3
GOCACHE=/tmp/arivu-build-cache go test ./internal/bookmarks -run '^$' \
  -bench BenchmarkDecorateSearchResultsFeedback -benchmem -benchtime=20x -count=3
```

For Graph's before measurement, copy `graph_relationship_batch_test.go` to a
foundation checkout and run the same benchmark. Notes and Search benchmarks
compare the retained singleton paths with the new batched paths in one build.

`go test ./...`, `go test -tags sqlite_fts5 ./...`, and
`go test -race ./internal/bookmarks` passed. The 33 frontend/extension tests and
10 capture tests passed. Formatting and diff checks passed. Browser checks
confirmed sign-in, 200 rendered note links, both endpoints in a focused Graph,
and the edited note in search. Frontend source did not change.

The existing vet finding and baseline-wide race documented above remain out
of scope. Ponytail Audit and CE Code Review were still unavailable.
