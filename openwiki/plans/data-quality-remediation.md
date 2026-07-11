# Arivu Data Quality Remediation Brief

> **Purpose:** This is an implementation brief for Codex Desktop. Open the Arivu repository in Codex Desktop, attach or reference this document, and ask Codex to implement it end to end. The brief is based on a read-only audit of a production SQLite snapshot and the exact Arivu v1.1.0 source.

## Codex task statement

You are working in the Arivu repository. Fix the fetch, summarization, semantic enrichment, and insight-quality pipeline end to end. Do not limit the work to prompt changes. The production audit in this document demonstrates structural problems in ingestion, evidence preservation, semantic extraction, insight eligibility, ranking, and evaluation.

Read `AGENTS.md` and `openwiki/quickstart.md` first. Inspect the current branch before editing because it may be newer than the audited v1.1.0 commit. Reconcile this brief with newer code rather than blindly replacing it. Preserve security properties, user isolation, manual data, and existing API compatibility where practical.

Deliver:

1. Schema migrations and provenance fields needed to distinguish source evidence, fetched content, generated summaries, and derived semantics.
2. Source-aware X and web ingestion that preserves authoritative evidence and records extraction quality.
3. Evidence-bounded, length-adaptive summaries with strict validation.
4. High-precision entity, concept, and tag extraction with provenance and confidence.
5. A redesigned insight candidate, eligibility, scoring, ranking, pagination, and feedback pipeline.
6. A safe dry-run audit and versioned reprocessing path for existing data.
7. Focused unit, integration, golden-fixture, API, and regression tests.
8. Updated OpenWiki documentation and `CHANGELOG.md`.

Do not declare the work complete merely because tests pass. Run the quality evaluation described below against representative fixtures and report the before/after results.

---

## 1. Audit scope and evidence

### Audited artifacts

- Production snapshot: `/home/ubuntu/arivu-audit.sqlite3`
- Snapshot audit date: 2026-07-11 UTC
- Installed application: Arivu v1.1.0
- Matching source commit: `872bf814935cd7e72a3a009142b4fc77b271f8b8`
- Matching source tag: `v1.1.0`
- Model configured during the audited run: `gemini-3-flash-preview`
- Source checkout used for diagnosis: `/home/ubuntu/arivu-source-v1.1.0`

The snapshot passed `PRAGMA quick_check`; the problem is behavioral quality, not SQLite corruption.

### Dataset shape

| Measure | Observed |
|---|---:|
| Bookmarks | 100 |
| X bookmarks | 99 |
| Web bookmarks | 1 |
| Completed AI summaries | 99 |
| Pending summaries | 1 |
| Successful embeddings | 99 |
| Tags | 491 |
| Bookmark-tag assignments | 793 |
| Entity assignments | 819 |
| Concept assignments | 792 |
| Distinct entities, case-insensitive | 508 |
| Distinct concepts, case-insensitive | 484 |
| Item links | 0 |
| Notes and daily notes | 0 |
| Annotations | 0 |
| Knowledge objects | 0 |
| Insight feedback rows | 0 |

The corpus is almost entirely a single bulk X import. All 100 bookmarks were created within approximately 32 seconds on 2026-07-10. Processing completed over the following 15 minutes.

### Fetch quality findings

| Measure | Observed |
|---|---:|
| Missing descriptions | 99/100 |
| Missing thumbnails | 100/100 |
| X items with `&quot;` still encoded in title | 93/99 |
| X items with encoded apostrophes in title | 22/99 |
| X texts containing scraped view metrics | 93/99 |
| X texts containing scraped display time | 93/99 |
| X texts containing an author handle | 97/99 |
| X items whose final domain is `x.com` | 93/99 |
| X items whose final domain is an external target | 6/99 |
| X texts shorter than 500 characters | 64/99 |
| X texts shorter than 280 characters | 10/99 |

The clean X API text initially inserted into `bookmarks.description` and `bookmarks.text_content` is later overwritten by the generic web fetch result. For direct X items, this means an HTML scrape replaces the authoritative API payload. The scrape adds author names, handles, timestamps, view counts, video timestamps, quoted-post fragments, and page-card text. For external-link items, the external article can replace the tweet context, while the original tweet text is not retained in a dedicated source-evidence field.

### Summary quality findings

| Measure | Observed |
|---|---:|
| Average fetched text length | 1,050 characters |
| Minimum fetched text length | 173 characters |
| Average one-sentence summary | 22.1 words |
| One-sentence summaries over 25 words | 0/99 |
| Average long-form summary | 134.3 words / 991.5 characters |
| Long-form summaries over 150 words | 11/99 |
| Long-form longer than fetched evidence | 79/99 |
| Long-form at least 2x fetched evidence | 62/99 |
| Long-form at least 3x fetched evidence | 31/99 |
| Long-form containing “practical takeaway” | 32/99 |
| Long-form containing “for developers” | 20/99 |
| Long-form containing a form of “demonstrates” | 20/99 |

The fixed 100-150 word output requirement forces expansion when the input is a short tweet. Representative failures include:

- A 173-character product showcase becomes a 991-character briefing containing unsupported claims about platform adoption, infrastructure, scalability, industry influence, and development volume.
- A tweet merely listing three named Codex skills becomes an explanation of what each skill supposedly does, even though those definitions are absent from the source.
- A tweet announcing a GLM 5.1 versus 5.2 comparison becomes a conclusion that GLM 5.2 is superior, although the fetched text contains no results.
- A short tweet about a Microsoft 4B code-exploration model becomes an organizational recommendation to adopt tiered model architectures.

These are not isolated wording issues. The output format asks for a core argument, evidence, and practical takeaway even when the evidence is only a short observation or list.

### Semantic enrichment findings

The application creates approximately 8.2 entities and 7.9 concepts per bookmark. Of 491 tags, 379 are used once. The most frequent derived concepts include:

| Concept | Bookmarks | Why it is invalid or low value |
|---|---:|---|
| `quot` | 35 | HTML entity residue from `&quot;` |
| `2026` | 25 | Scraped date token |
| `all` | 10 | Generic function word |
| `article` | 11 | X page-card chrome |
| `any` | 7 | Generic function word |
| `just` | 5 | Generic function word |
| `jun` | 4 | Scraped date token |
| `com` | 3 | URL residue |

The most frequent entity is `Quot`, assigned to 93 bookmarks. `Https` appears on 31. Other “entities” include `All`, `Any`, `Ago`, `Ask`, and `Every`.

This occurs because `internal/bookmarks/enrichment.go` does not perform entity or concept extraction. It tokenizes the title and body, removes a very small stopword list, ranks raw token frequency, title-cases title tokens as entities, and stores the same frequent tokens as concepts and enrichment tags. The structured AI summary’s suggested tags do not drive graph enrichment.

### Current insight output

The audited database yields the following deterministic candidates:

| Insight family | Candidates before API limit |
|---|---:|
| Emerging theme | 106 |
| Recurring connection | 19 |
| Changed thinking | 0 |
| Forgotten value | 0 |
| Knowledge gap | 0 |
| Serendipitous connection | 20 returned from 179 eligible raw pairs |
| Total | 145 |

The UI requests `/api/insights?limit=40`. The backend builds every family, sorts by type and stable hash ID, then applies the global limit. With no changed-thinking candidates, `emerging_theme` sorts first and has 106 candidates. Therefore all 40 visible results are arbitrary emerging-theme tokens. Examples from the actual first page include:

`cache`, `com`, `app`, `they`, `cursor`, `work`, `git`, `ask`, `search`, `building`, `hardware`, `shipping`, `just`, `company`, `moe`, `india`, `google`, `add`, `break`, `200`, `build`, `prompt`, `tamil`, `any`, `run`, `jun`, `write`, `2025`, `don`, `first`, `easy`, `grill`, `dev`, `pagerduty`, `install`, `skill`, `attention`, `costs`, `model`, `how`.

Every emerging-theme candidate receives 100% confidence because all bookmarks have a recent `updated_at` and no prior-period count. This incorrectly treats “all observations were loaded recently” as “the theme is certainly emerging.”

The family dropdown filters the already limited client-side result. Consequently, selecting recurring or serendipitous families can produce an empty page even though those candidates exist beyond the first 40.

---

## 2. Root-cause map in v1.1.0

### 2.1 X API evidence is fetched but not preserved

Relevant code:

- `internal/providers/x.go:23-29`
- `internal/providers/x.go:133-148`
- `internal/app/x.go:239-307`
- `internal/app/x.go:388-403`

Problems:

1. `XBookmark` does not retain `created_at` even though it is requested.
2. The API request does not request or expand enough context for long-note tweets, media, attachments, or referenced tweets.
3. `insertXBookmark` correctly starts with `tweet.Text`, but enqueues the generic `bookmark.process` job against the selected URL.
4. Direct X URLs are then scraped as generic HTML.
5. External links can become the bookmark URL, but no independent field preserves the source post text and publication time.
6. X author identity is stored, but insight diversity uses `bookmarks.domain`, which is `x.com` for most authors.

### 2.2 Generic processing overwrites better evidence

Relevant code:

- `internal/bookmarks/import_export.go:59-106`

Problems:

1. The same fetch-and-replace behavior is used for direct web pages, direct X posts, and X posts linking elsewhere.
2. `description`, `sanitized_html`, and `text_content` are overwritten without preserving source-specific evidence.
3. There is no content hash, extraction method, quality reason, or version.
4. Reprocessing cannot distinguish manually corrected content from generated or fetched content.

### 2.3 Fetch quality is effectively binary and permissive

Relevant code:

- `internal/safefetch/safefetch.go:116-154`
- `internal/safefetch/safefetch.go:232-244`
- `internal/safefetch/safefetch.go:319-347`

Problems:

1. Any nonempty text is “complete” unless it contains exactly `discussion about this post`.
2. A test explicitly accepts `Fixed login redirect handling.` as complete without source context.
3. The title extractor returns raw HTML text rather than unescaping entities.
4. The largest `div`, `section`, or `body` can win, which is weak for client-rendered social pages.
5. There is no boilerplate ratio, evidence-token count, repeated-chrome detection, or source-aware policy.

### 2.4 The summary prompt forces invention

Relevant code:

- `internal/providers/gemini.go:39-55`
- `internal/providers/gemini.go:178-194`
- `internal/providers/gemini.go:197-287`
- `internal/providers/gemini.go:400-440`

Problems:

1. Every input is labeled `ARTICLE`, including tweets.
2. Every input must produce a 100-150 word executive briefing in two paragraphs.
3. Every input must produce 3-5 bullets, 4-6 highlights, and 4-6 tags.
4. The prompt explicitly requests a practical takeaway even if none exists.
5. Provider calls do not use a JSON response schema or deterministic generation settings where supported.
6. Parsing validates only JSON types. It does not enforce lengths, counts, tag syntax, uniqueness, evidence support, number/name provenance, or redundancy.
7. There is no output-to-evidence ratio guard and no faithful fallback when validation fails.

### 2.5 “Concepts” and “entities” are token frequency

Relevant code:

- `internal/bookmarks/enrichment.go:23-59`
- `internal/bookmarks/enrichment.go:68-138`
- `internal/bookmarks/enrichment.go:176-198`

Problems:

1. `keyTerms(body, 8)` becomes tags and concepts.
2. `titleTerms(..., 10)` becomes entities.
3. Title words are weighted by three, amplifying X title chrome and HTML entity residue.
4. The stopword list is too small for this use and cannot turn bag-of-words into semantics.
5. There is no canonical label, alias resolution, entity type, confidence, provenance, or enrichment version.
6. The model’s `suggested_tags` are stored in `ai_summaries` but not used for graph concepts.
7. The system always emits terms even when it has no reliable semantic extraction. Missing semantics would be safer than fabricated graph structure.

### 2.6 Insight detectors promote ingestion artifacts

Relevant code:

- `internal/bookmarks/insights.go:31-65`
- `internal/bookmarks/insights.go:88-138`
- `internal/bookmarks/insights.go:143-203`
- `internal/bookmarks/knowledge_graph.go:351-353`
- `internal/app/web/app.js:4445-4464`

Problems:

1. Emerging themes use bookmark `updated_at`, not source publication or observation time.
2. Bulk imports and reprocessing look like topic emergence.
3. The emergence rule is only two recent bookmarks and more recent than prior.
4. No minimum baseline volume is required.
5. No author, publisher, or source diversity is required.
6. Confidence is the recent share of recent plus prior, so an empty baseline produces 1.0.
7. Recurrence and serendipity use domain diversity, but `x.com` is a platform, not a publisher or author.
8. Any shared raw token can create a connection.
9. Serendipitous results are the first 20 in SQL lexical/ID order, not the best 20.
10. The global sort is by type and hash ID, not relevance, novelty, evidence strength, or time.
11. The API applies the limit before the UI family filter.
12. There is no family quota, cursor, or candidate score.
13. Feedback hides individual stable IDs but is not used to calibrate detectors or extraction quality.

---

## 3. Product definitions: what counts as an insight

These definitions are acceptance criteria, not merely UI copy.

### Source evidence

Authoritative or extracted material that the system can point to. Examples: X API post text, an article paragraph, an annotation, an authored note, a transcript segment, or OCR text. Source metadata and engagement metrics are context, not substantive evidence unless the claim is specifically about those metrics.

### Summary

A faithful compression of one item. It must not add causes, effects, recommendations, definitions, comparisons, adoption claims, or practical implications absent from the evidence.

### Key point

An atomic claim directly supported by one item. Multiple key points may come from the same source. A key point is still not an insight.

### Highlight

Prefer an extractive passage copied from the source, with a source locator or text offsets. If the product wants abstractive highlights instead, rename them `key_points`; do not present generated interpretations as highlights. Do not maintain both bullets and highlights if they convey the same information.

### Tag

A user-facing organizational label. It may be broad and should be stable enough to reuse. A tag is not automatically a graph concept.

### Entity

A concrete named person, organization, product, project, place, standard, model, or other typed proper noun supported by the evidence. `Quot`, `Https`, `Any`, and arbitrary title words are not entities.

### Concept

A normalized, meaningful noun phrase representing an idea discussed by the evidence, such as `row-level security`, `codebase exploration`, or `animation vocabulary`. A concept is not a single frequent token unless that token is independently meaningful and disambiguated.

### Relationship candidate

A proposed connection between two items based on a specific shared concept, explicit link, embedding similarity, or user action. It is not itself an insight until it passes evidence and usefulness gates.

### Insight

An insight is a novel, specific, evidence-backed synthesis or user-relevant observation that would not be obtained by simply rereading one source title. It must satisfy all of the following:

1. **Supported:** every factual part is traceable to displayed evidence.
2. **Specific:** it names the concrete topic, change, tension, recurrence, or gap.
3. **Nontrivial:** it is not a tag count, token recurrence, import fact, or paraphrased source.
4. **Appropriately synthesized:** cross-source insights normally require at least two independent sources; single-source insights require explicit authored reflection or a high-value user signal.
5. **Novel enough:** it is not a duplicate of a recently shown insight or an obvious restatement.
6. **Useful:** it supports a review, connection, decision, question, or action that fits Arivu’s second-brain purpose.
7. **Calibrated:** confidence reflects evidence quality and uncertainty, not merely a ratio with a zero baseline.

### Items that should not be called insights

- “`quot` is emerging.”
- “You saved two items about code.”
- A summary or a generated practical takeaway from one tweet.
- A reminder to revisit an old bookmark. This is a resurfacing recommendation.
- An important item with no concepts. This is an enrichment or knowledge-gap task.
- Two X posts sharing a generic word. This is a low-quality relationship candidate.
- A source engagement metric unless the user is analyzing engagement.

Keep resurfacing recommendations, enrichment tasks, and knowledge hygiene suggestions in separate product surfaces or clearly label them as recommendations rather than insights.

---

## 4. Target pipeline

Implement an explicit staged pipeline:

```text
capture
  -> preserve source envelope
  -> source-aware fetch/expansion
  -> extraction quality assessment
  -> evidence selection
  -> summary generation
  -> summary validation
  -> semantic extraction
  -> semantic validation/canonicalization
  -> versioned persistence
  -> insight candidate generation
  -> eligibility gates
  -> scoring/deduplication/diversification
  -> API pagination and feedback
```

Each stage must have a version, status, and failure reason. A downstream stage must be able to refuse low-quality upstream evidence without destroying the saved bookmark.

---

## 5. Required implementation work

### Workstream A: Preserve evidence and provenance

Add a migration using names compatible with the current schema. The exact schema can differ, but the data model must represent:

- Original source type (`web`, `x`, import type, document type).
- Original capture URL and canonical content URL.
- Source-native text, especially X API text.
- Source publication timestamp distinct from capture, processing, and user-update timestamps.
- Source author/publisher identity distinct from web domain.
- Fetched/extracted content and extraction method.
- Content quality status and structured quality reasons.
- Content hash.
- Fetch/extractor version.
- Summary version and semantic-enrichment version.
- Whether a field is source-provided, fetched, generated, fallback, or manually edited.

A reasonable incremental design is to retain `bookmarks.text_content` as the selected reader content while adding fields such as:

```text
source_text
source_published_at
source_author_id
source_publisher_key
canonical_url
content_kind
content_quality
content_quality_reasons_json
content_hash
fetch_method
fetch_version
summary_version
enrichment_version
```

Use a separate evidence table if it results in a cleaner model, particularly if one X post can have post text, quoted-post text, linked-article text, and media transcript evidence. Do not create a large abstraction merely to avoid a migration; choose the smallest model that preserves distinct evidence correctly.

Requirements:

1. Never overwrite authoritative source-native text with a lower-authority scrape.
2. Never overwrite manually edited fields during reprocessing.
3. Retain original X post context when an external article is fetched.
4. Distinguish `captured_at`, `source_published_at`, `processed_at`, and user `updated_at`.
5. Add indexes needed for source-time insight windows and versioned reprocessing.
6. Include new provenance fields in full export/restore with backward compatibility.

### Workstream B: Source-aware X processing

Change X ingestion so direct X posts do not go through the generic X webpage scraper.

1. Extend `providers.XBookmark` to retain `created_at` and supported long-form/note-tweet fields.
2. Request supported attachment, media, referenced-post, and note-tweet fields and expansions where the X API plan permits them.
3. Treat the API post text as authoritative source evidence.
4. Build a readable X content representation without view counts, display timestamps, navigation, or duplicated author chrome.
5. If a post links to an external URL, preserve both the post evidence and external fetch evidence.
6. If the external article fetch is complete, use the article as primary summary evidence and the post as source context, without blending unsupported post commentary into article claims.
7. If the external fetch is partial or fails, summarize only the X post if it has enough evidence; otherwise mark the item metadata-only or partial.
8. For link-only, media-only, or video-only posts, do not invent a summary. Use available title/card metadata, OCR, transcript, or an explicit insufficient-evidence state.
9. Use X author ID or username as publisher identity for diversity calculations, not `x.com`.
10. Preserve `x_metrics_json` as contextual metadata but exclude it from normal semantic extraction and summarization.

### Workstream C: Extraction quality

Replace `contentQuality(text string)` with a source-aware assessment that returns status, score, and reasons. It should consider:

- Evidence origin: API, article extraction, plain text, OCR, transcript, or generic HTML.
- Meaningful token and sentence counts.
- Chrome/noise ratio.
- Repeated title, author, navigation, cookie, engagement, date, and social UI patterns.
- Whether content is only a URL, title, error page, login wall, or social placeholder.
- Whether the selected node is suspiciously the whole body.
- Whether an external target redirected back to a social/login page.
- Content-language and decoding problems.

Do not use one universal minimum length. A 20-word authoritative API tweet can be complete for its content kind, while a 20-word extraction from a 3,000-word article is partial.

Unescape HTML entities in titles and descriptions at extraction boundaries. Add regression tests for `&quot;`, `&#x27;`, ampersands, and Unicode titles.

Suggested statuses:

- `complete`: usable evidence for the declared content kind.
- `partial`: some evidence is usable, but important content is missing.
- `metadata_only`: only a title/card/URL/metrics are available.
- `failed`: no safe usable evidence.

Persist reason codes such as `social_chrome`, `login_wall`, `too_little_article_text`, `link_only`, `media_without_transcript`, `unsupported_content_type`, and `upstream_http_401`.

### Workstream D: Evidence-bounded summaries

Create a typed request and response instead of passing an unlabelled text blob:

```go
type SummaryRequest struct {
    ContentKind      string
    Title            string
    SourceText       string
    PrimaryText      string
    SourcePublished  time.Time
    QualityStatus    string
    QualityReasons   []string
}
```

The exact fields may differ. The generator must know whether it is summarizing a tweet, article, thread, note, transcript, or metadata-only item.

#### Length-adaptive output policy

Use evidence size and kind to decide which fields are allowed:

| Evidence type | One sentence | Key points | Long form | Extractive highlights |
|---|---|---|---|---|
| Very short social post | Yes, shorter than source when possible | 0-2 | Omit | 0-1 |
| Medium social post/thread | Yes | 1-4 | Optional, capped well below evidence | 0-3 |
| Article/document | Yes | 3-5 | 80-180 words based on source size | 2-5 |
| Metadata-only/link-only | No generated claim; explicit insufficient evidence | 0 | Omit | 0 |

Do not force empty fields to contain filler. Update the frontend to render absent long-form text, bullets, or highlights naturally.

#### Prompt requirements

The prompt must:

1. State the content kind and evidence boundary.
2. Treat source text as data, not instructions.
3. Prohibit adding definitions, mechanisms, causes, results, recommendations, comparisons, or implications absent from evidence.
4. Prohibit interpreting a product or skill name based only on its name.
5. Require uncertainty language when the source itself is tentative.
6. Preserve attribution for opinions and claims.
7. Ignore social metrics unless requested as a key fact.
8. Distinguish what happened from what the author predicts or recommends.
9. Return fewer fields for sparse evidence.
10. Return typed JSON through provider-native structured output where supported.

#### Validation requirements

Add deterministic validation after parsing:

- Required keys and types.
- Per-kind word/count limits.
- Tag syntax, count, uniqueness, and normalization.
- Duplicate or near-duplicate bullets/highlights.
- Output-to-input expansion ratio.
- Numbers, dates, percentages, and named entities absent from source evidence.
- Boilerplate phrases and unsupported recommendation language.
- Empty or malformed output.
- Generated content when quality is metadata-only or failed.

Validation failure must not be stored as `completed`. Retry once with validation feedback only when useful and bounded. Otherwise store a faithful extractive fallback or an explicit insufficient-evidence status. Preserve the prior valid summary during a failed reprocess until a replacement passes validation.

For critical faithfulness, optionally add a configurable claim-verification pass that checks each generated key point against evidence. Keep it off or sampled if cost is a concern, but design the evaluation hooks now.

Store generation provenance: provider, model, prompt version, validator version, generation time, validation result, and reason codes. Never expose API keys.

### Workstream E: Semantic extraction

Stop storing frequency tokens as entities and concepts.

Implement typed semantic output, ideally as part of the same structured model call when a validated summary is generated:

```json
{
  "entities": [
    {"name": "Microsoft", "type": "organization", "confidence": 0.98, "evidence": "..."}
  ],
  "concepts": [
    {"label": "codebase exploration", "confidence": 0.91, "evidence": "..."}
  ],
  "suggested_tags": ["codebase-exploration"]
}
```

Requirements:

1. Emit zero terms when evidence is insufficient.
2. Cap entities and concepts based on evidence length; do not target a fixed eight or ten.
3. Require a supporting source span for every model-derived entity and concept.
4. Normalize case, punctuation, Unicode, singular/plural variants, and obvious aliases.
5. Keep display label separate from normalized key.
6. Store type, confidence, extraction method, evidence locator, and enrichment version.
7. Maintain manual tags separately and never delete them during reprocessing.
8. Do not automatically make every suggested tag a graph concept.
9. Add an explicit denylist for known extraction artifacts as a last-line guard, including HTML entity fragments, URL components, date fragments, social chrome, and high-frequency generic words.
10. Use document-frequency suppression for corpus-wide junk, but do not rely on it as the primary semantic method.
11. If no model is configured, prefer no graph semantics or a conservative, clearly marked fallback over the current bag-of-words graph.

Backfill and migration must remove only generated/enrichment terms from the affected enrichment version. Preserve manual tags and user-confirmed links.

### Workstream F: Insight redesign

Split insight processing into explicit phases:

```text
candidate generation -> eligibility -> scoring -> deduplication -> diversification -> pagination
```

Persisting candidates is optional, but the design must expose scores and rejection reasons in a quality audit.

#### Global eligibility gates

All insight families should require:

- Valid underlying content quality.
- Valid concept/entity provenance.
- Minimum semantic confidence.
- Displayable evidence with stable links.
- No hidden/dismissed equivalent.
- No near-duplicate recently shown insight.
- No extraction artifact or generic concept.

#### Emerging themes

An emerging theme should normally require:

1. A specific normalized concept.
2. At least three qualifying items.
3. At least two independent publishers/authors.
4. Source publication/observation time, never processing `updated_at`.
5. A meaningful comparison baseline with enough corpus activity in both windows.
6. A rate or share increase, not only a raw count increase caused by batch size.
7. A minimum absolute lift and minimum score.

If the corpus was just bulk imported and has no usable historical baseline, return “not enough history” rather than assigning 100% confidence.

#### Recurring connections

Require a specific concept supported across at least three items and at least two independent publishers or contexts. Exclude platform domains as publisher identity. Prefer recurrence across time or projects, not multiple captures from one import minute. The explanation must say what recurs, not merely that a token repeats.

#### Serendipitous connections

Generate candidates from specific shared concepts plus meaningful contextual difference. Consider embedding distance, source type, author, collection, and time. Suppress generic shared concepts. Rank all candidates; do not take the first SQL rows. Limit repeated pairs and repeated concepts.

#### Changed thinking

Keep this family grounded in user-authored notes, but require a meaningful old/new position or explicit revision context. Phrase matching alone can create false positives such as quoted text. Evidence should show the relevant note excerpt and, when possible, the prior position.

#### Forgotten value and knowledge gaps

Move these to recommendations, review, or knowledge-hygiene surfaces unless the product intentionally uses “insights” as an umbrella term. They are operational prompts, not synthesized knowledge. If retained in the endpoint, give them a distinct `kind: recommendation` and do not mix their confidence with analytical insight confidence.

#### Scoring

Use an inspectable score with components such as:

- Evidence quality.
- Semantic specificity.
- Source/author diversity.
- Temporal strength.
- Novelty.
- User relevance: importance, collection, notes, annotations, prior feedback.
- Actionability.
- Penalties for genericity, same-batch concentration, same-author concentration, and extraction uncertainty.

Do not present a mathematical ratio as epistemic confidence unless it is calibrated. Consider displaying `evidence strength` with low/medium/high labels until enough feedback exists to calibrate probabilities.

#### Ranking and API behavior

1. Rank by score, not type plus stable hash.
2. Apply a configurable family quota or diversity pass so one detector cannot fill the page.
3. Accept a server-side `family` filter and apply it before the limit.
4. Add stable cursor pagination.
5. Return `score_components`, `eligibility_reason`, detector version, and evidence metadata in debug/audit mode.
6. Keep stable IDs for feedback, but include detector/version inputs so materially changed algorithms do not inherit inappropriate dismissals.
7. Make UI empty states distinguish “no qualifying insights” from “not enough history” and “items need reprocessing.”

### Workstream G: Feedback and evaluation signals

The audited database has no insight feedback despite 145 candidates. Improve the loop:

1. Track `shown_at` separately from explicit feedback so acceptance denominators are known.
2. Track detector family and version with impressions and feedback.
3. Keep `useful`, `not_useful`, `dismiss`, and `snooze` semantics distinct.
4. Use dismiss/not-useful signals to suppress near-duplicates and diagnose bad concepts.
5. Do not claim that feedback “learns” unless it changes ranking or extraction behavior.
6. Add a compact reason picker for not-useful feedback if appropriate: unsupported, obvious, generic, wrong connection, stale, or bad source.
7. Ensure feedback remains user-scoped and exportable.

### Workstream H: Audit and reprocessing tooling

Add a safe CLI or admin command that can run against SQLite and emit a redacted quality report. Suggested interface:

```bash
arivu quality audit --db arivu.sqlite3 --format json
arivu reprocess --db arivu.sqlite3 --stale-version --dry-run
arivu reprocess --db arivu.sqlite3 --stale-version --batch-size 25
```

Names may follow existing CLI conventions. The audit should report:

- Counts by source, content kind, quality status, and reason.
- Missing evidence and provenance fields.
- Summary status and validation failures.
- Expansion ratio distribution.
- Semantic terms per item and singleton rate.
- Known junk terms and high-document-frequency terms.
- Insight candidates, eligible results, rejection reasons, and family distribution.
- Feedback impressions and rates when available.
- Enrichment, prompt, validator, and detector version drift.

The reprocessor must:

1. Require or strongly recommend a consistent backup.
2. Support dry run.
3. Be resumable and idempotent.
4. Use bounded batches and the durable jobs queue.
5. Preserve manual titles, descriptions, tags, notes, annotations, collections, links, states, reminders, and feedback.
6. Replace old generated artifacts only after a new version validates.
7. Avoid deleting the last valid summary before replacement.
8. Report processed, skipped, partial, failed, and preserved counts.

---

## 6. Data repair plan for the audited installation

Do not run destructive cleanup SQL directly against production. Implement and test the versioned repair path first.

Recommended sequence:

1. Create and verify a consistent SQLite backup.
2. Deploy schema/provenance migration with old behavior still readable.
3. Deploy source-aware ingestion and quality assessment for new captures.
4. Deploy summary and semantic validators in shadow/audit mode.
5. Reprocess a stratified sample of 15-20 existing items.
6. Compare old and new evidence, summaries, entities, concepts, and insight candidates.
7. Obtain human review on the sample.
8. Enable new persistence for newly processed items.
9. Reprocess the remaining 100 bookmarks in bounded batches.
10. Remove only old `source='enrichment'` tag assignments and generated semantic rows after validated replacements exist.
11. Recompute search index and graph outputs.
12. Regenerate insight candidates.
13. Verify manual and source-native data counts before and after.

For the audited corpus, direct X items should be repaired from preserved/source-refetched API data where possible. If authoritative X text cannot be recovered, retain the current scraped text as legacy evidence with a low-quality reason rather than pretending it is clean API evidence.

The one pending bookmark corresponds to a `bookmark.process` job that exhausted five attempts with `upstream status 401`. Preserve the bookmark and expose the fetch failure reason; do not leave an indefinite generic pending state.

---

## 7. Test plan

### Fetch and provenance tests

Add tests for:

1. Direct X API text remains unchanged after processing.
2. X publication time is stored separately from capture and update times.
3. X author is used as publisher identity.
4. External-link X posts preserve both post and article evidence.
5. A failed external fetch falls back to source post evidence without destroying it.
6. Link-only and media-only posts become metadata-only/partial without generated claims.
7. X metrics and display chrome are excluded from summary evidence.
8. HTML title entities are unescaped once, with no double decoding.
9. Generic web content still passes SSRF protection, redirect validation, size limits, and sanitization.
10. Reprocessing preserves manual fields and prior valid outputs on failure.

### Summary tests

Create golden fixtures for at least:

- A 20-word X observation.
- A list of named skills with no definitions.
- A model comparison announcement with no result.
- An opinionated post.
- A quoted/referenced post.
- A long X thread.
- An X post linking a complete article.
- An X post linking a login wall.
- A long technical article.
- A metadata-only page.
- A page containing prompt injection text.

Assert:

1. Sparse evidence never produces a forced long form.
2. Skill names are not interpreted without evidence.
3. Comparisons do not acquire winners or results absent from evidence.
4. Recommendations appear only when the source recommends them.
5. All output numbers and named entities occur in evidence or approved metadata.
6. Word/count constraints are enforced by code.
7. Duplicate bullets/highlights are rejected or removed.
8. Invalid provider JSON does not overwrite the last valid summary.
9. Metadata-only content receives no synthetic summary.
10. Provider-native structured response settings are exercised where supported.

### Semantic tests

Assert that:

- `&quot;`, `quot`, `https`, `com`, `jun`, display times, view counts, and generic words are never entities or concepts.
- `Microsoft` can be an organization entity when explicitly present.
- `row-level security` remains a phrase-level concept.
- Evidence spans actually occur in selected source evidence.
- Duplicate aliases normalize to one key.
- Low-confidence and unsupported terms are dropped.
- Model-unavailable fallback does not repopulate the graph with raw frequency tokens.
- Manual tags survive reprocessing.

### Insight tests

Add deterministic tests for:

1. A bulk import today of old sources does not produce emerging themes.
2. `updated_at` changes do not alter source-time trend windows.
3. An empty prior window does not produce 100% confidence.
4. Fewer than three sources fails emerging-theme eligibility.
5. Multiple posts by one X author do not satisfy source diversity.
6. Different X authors do satisfy publisher diversity even though the domain is the same.
7. Generic concepts never create recurring or serendipitous insights.
8. Serendipitous candidates are scored before limiting.
9. Global results contain family diversity when multiple families qualify.
10. `family=recurring_connection&limit=40` filters server-side before limiting.
11. Cursor pagination is stable across requests.
12. Dismissed insights and their near-duplicates are suppressed appropriately.
13. Detector-version changes handle old feedback deliberately.
14. Recommendations are typed separately from analytical insights.
15. Evidence from another user never leaks.

### Migration and repair tests

Test upgrades from a representative pre-migration database. Verify:

- Existing bookmarks remain readable.
- Old full exports still restore.
- New exports round-trip provenance and versions.
- Manual and generated tags remain distinguishable.
- Reprocessing is idempotent.
- Interrupted batches resume.
- Failed replacements leave prior valid summaries intact.
- Search and graph indexes refresh only after successful replacement.

---

## 8. Evaluation dataset and rubric

Build a checked-in, privacy-safe evaluation set from synthetic or de-identified fixtures. Do not commit production bookmark contents.

Use at least 30 fixtures, stratified across:

- Short direct X posts.
- Long posts/threads.
- Quoted or referenced posts.
- Link-only and media-only posts.
- X-to-article links.
- Documentation and technical articles.
- Marketing pages.
- Login walls and partial extractions.
- Pages with tables, code, or unusual Unicode.
- Sources containing prompt-injection text.

For each fixture, store expected:

- Content kind and authoritative evidence.
- Quality status and reason codes.
- Facts that may appear in a summary.
- Facts/inferences that must not appear.
- Valid entities, concepts, and aliases.
- Whether it is eligible for each insight family in a small corpus scenario.

Human rating dimensions, each on a documented scale:

1. Fetch completeness.
2. Noise/chrome contamination.
3. Summary faithfulness.
4. Summary coverage.
5. Summary concision.
6. Redundancy.
7. Entity precision.
8. Concept precision.
9. Insight support.
10. Insight specificity and usefulness.

Keep a machine-checkable subset for CI and a human-reviewed benchmark for release decisions.

---

## 9. Acceptance gates

The implementation is not complete until these gates pass on the evaluation set and a safe production dry run.

### P0 correctness gates

- 100% of direct X fixtures preserve API source text exactly.
- 100% of source publication timestamps remain distinct from processing timestamps.
- Zero raw HTML entity fragments, URL fragments, display times, or social metrics become entities or concepts.
- Zero summaries are generated for failed or metadata-only evidence.
- Zero unsupported numbers or named entities in machine-checkable summary fixtures.
- Zero forced long-form summaries for very short social evidence.
- Manual data is unchanged by reprocessing tests.
- User isolation and SSRF/security tests continue to pass.

### Summary quality targets

- At least 90% of human-rated summaries score faithful and useful.
- No more than 5% contain unsupported causal, comparative, adoption, or recommendation language.
- For evidence under 500 characters, generated prose should normally be shorter than the evidence and must never exceed 1.5x without an explicit validated reason.
- Bullet/highlight near-duplicate rate below 10%; prefer removal of the redundant field.

### Semantic quality targets

- Entity precision at least 95% on labeled fixtures.
- Concept precision at least 90% on labeled fixtures.
- Median generated concepts per short social post no greater than three.
- Corpus singleton rate is monitored, not optimized blindly; singletons must still be meaningful.
- Known-junk term count is zero.

### Insight quality targets

- Every displayed insight has visible, qualifying evidence.
- No emerging theme is based on ingestion or reprocessing time.
- No confidence of 100% is produced solely by an empty baseline.
- When three or more families qualify, the first page contains more than one family.
- Server-side family filters return that family even when another family has more candidates.
- At least 70% of a human-reviewed initial sample is rated useful; track the real rate after launch.
- Not-useful reasons and impression denominators are measurable.

---

## 10. Observability and quality reporting

Add privacy-conscious operational metrics or audit counters for:

- Fetch attempts and outcomes by source/content kind.
- Extraction quality status and reasons.
- Summary generation, validation, retry, fallback, and rejection counts.
- Prompt/model/validator versions.
- Output-to-input ratio buckets.
- Semantic terms emitted, rejected, canonicalized, and suppressed.
- Insight candidates generated, rejected, shown, and acted upon by family/version.
- Feedback rates with impression denominators.
- Reprocessing progress and preservation counts.

Do not log raw private content by default. Logs should carry bookmark/job IDs, source type, versions, status, and reason codes. Provide an opt-in local debug mode for inspecting evidence and validation decisions.

---

## 11. Recommended delivery phases

### Phase 0: Stop amplification

- Suppress known junk concepts from insights immediately.
- Apply family filtering server-side.
- Rank before limiting and prevent one family from monopolizing results.
- Stop calling zero-baseline ratios 100% confidence.
- Mark the failed/pending job accurately.

This is a containment release, not the final fix.

### Phase 1: Evidence and fetch correctness

- Add provenance migration.
- Preserve X API evidence and timestamps.
- Separate post and linked-article evidence.
- Add source-aware quality assessment and reason codes.
- Fix HTML entity decoding.

### Phase 2: Summary correctness

- Introduce typed, content-aware summary requests.
- Add adaptive output policy and evidence-bounded prompt.
- Add structured response and strict validators.
- Update UI for absent optional fields.

### Phase 3: Semantic correctness

- Replace token frequency with evidence-backed semantic extraction.
- Add normalization, confidence, provenance, and versions.
- Stop unsafe fallback graph generation.

### Phase 4: Insight correctness

- Implement candidate eligibility, scoring, diversification, and pagination.
- Use source publication time and publisher identity.
- Separate recommendations from insights.
- Add impressions and useful/not-useful reason signals.

### Phase 5: Repair and rollout

- Add quality audit and versioned reprocessing commands.
- Run sample shadow comparison.
- Review, then reprocess the corpus in batches.
- Publish before/after metrics and remaining limitations.

Each phase should be independently deployable and reversible. Use feature flags or version gates where a schema migration and reprocessing job span releases.

---

## 12. Documentation changes

Update the relevant OpenWiki source documents and `CHANGELOG.md` to explain:

- Evidence/provenance model.
- Source-aware X processing.
- Content quality statuses.
- Summary and semantic-enrichment versions.
- Definition of summaries, highlights, concepts, relationships, insights, and recommendations.
- Insight eligibility and confidence/evidence-strength semantics.
- Quality audit and reprocessing commands.
- Backup, rollout, and rollback procedure.
- Provider limitations and behavior when no AI model is configured.

Do not describe feedback as learning unless the implementation actually uses it to alter future ranking or suppression.

---

## 13. Verification commands

Follow repository guidance and adapt paths to the current environment. At minimum run:

```bash
gofmt -w <changed-go-files>
GOCACHE=/private/tmp/arivu-build-cache go test ./...
node --check internal/app/web/app.js
node --check internal/app/web/sw.js
node --test internal/app/webtest/service-worker-register.test.mjs
node extension/url-utils.test.mjs
node extension/content.test.mjs
go build -trimpath -ldflags="-s -w" -o ./arivu ./cmd/arivu
```

Also run:

- Migration tests from a pre-change fixture.
- The new quality evaluation suite.
- A dry-run audit against a copy of the production snapshot.
- A small versioned reprocess against a disposable copy.
- API tests for server-side family filtering and pagination.
- Browser smoke checks for bookmark summaries and insight family views.

Do not run repair commands against the live `/var/lib/arivu/arivu.sqlite3` during development.

---

## 14. Required final handoff from Codex Desktop

The final implementation response should include:

1. Root causes fixed, grouped by pipeline stage.
2. Schema and API compatibility notes.
3. Files changed.
4. Tests and quality evaluations run, with exact results.
5. Before/after quality metrics.
6. Safe production migration and reprocessing commands.
7. Backup and rollback steps.
8. Known limitations, especially X API-plan limitations and model-provider differences.
9. Any acceptance gate not met, stated explicitly.

Do not report only code coverage or test counts. The core success criterion is that stored evidence is trustworthy, summaries remain within that evidence, semantic labels are meaningful, and displayed insights are specific, supported, ranked, and useful.
