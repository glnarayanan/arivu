import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

const app = await readFile(new URL("../web/app.js", import.meta.url), "utf8");
const styles = await readFile(new URL("../web/styles.css", import.meta.url), "utf8");
const publicShare = await readFile(new URL("../web/public-share.js", import.meta.url), "utf8");
const publicShareStyles = await readFile(new URL("../web/public-share.css", import.meta.url), "utf8");
const shareHandlers = await readFile(new URL("../../bookmarks/shares.go", import.meta.url), "utf8");

test("declares the five canonical knowledge destinations", () => {
  assert.ok(app.includes(`const nav = [
    ["/today", "Home"],
    ["/library", "Library"],
    ["/notes", "Notes"],
    ["/graph", "Graph"],
    ["/insights", "Insights"],
  ];`));
  for (const contract of [
    '{ prefix: "/today", page: todayPage',
    '{ prefix: "/library", page: libraryPage',
    '{ prefix: "/graph", page: graphPage',
    '{ prefix: "/insights", page: insightsPage',
    '{ prefix: "/search", page: searchPage',
    '["/today", "Home"]',
    '["/library", "Library"]',
    '["/notes", "Notes"]',
    '["/graph", "Graph"]',
    '["/insights", "Insights"]',
  ]) assert.ok(app.includes(contract), `missing frontend contract: ${contract}`);
  assert.ok(!app.includes('{ label: "Notes", action: () => navigate("/notes") }'));
});

test("keeps legacy deep links as explicit query-preserving aliases", () => {
  for (const route of ["dashboard", "knowledge-graph", "analytics", "inbox", "focus", "review", "board", "assistant", "objects", "evolution", "duplicates"])
    assert.match(app, new RegExp(`prefix: ["']/${route.replace("-", "\\-")}["']`));
  assert.ok(app.includes("function compatibilityRedirect"));
  assert.ok(app.includes("new URLSearchParams(location.search)"));
  assert.ok(app.includes("function focusCompatibilityRedirect()"));
  assert.ok(app.includes('params.set("focus", legacyFilter)'));
  assert.ok(app.includes('page: () => homeViewRedirect("review")'));
  assert.ok(app.includes('page: () => homeViewRedirect("board")'));
});

test("keeps Home navigation and purpose-built layouts across every view", () => {
  for (const active of ["pulse", "focus", "review", "board"])
    assert.ok(app.includes(`homeViewTabs("${active}")`), `missing Home tabs for ${active}`);
  for (const contract of [
    '["pulse", "Pulse", "/today"]',
    '["focus", "Focus", "/today?view=focus"]',
    '["review", "Review", "/today?view=review"]',
    '["board", "Board", "/today?view=board"]',
  ]) assert.ok(app.includes(contract), `missing Home tab contract: ${contract}`);
  assert.ok(app.includes('params.get("focus")'));
  assert.ok(app.includes('href="/today?view=focus&amp;focus=${name}"'));
  assert.ok(app.includes('class="review-grid" aria-label="Review queue"'));
  assert.ok(app.includes('class="board-scroller" role="region" aria-label="Knowledge workflow board" tabindex="0"'));
  assert.ok(app.includes('class="home-pulse-columns"'));
  assert.ok(app.includes('"pulse-captures"'));
  assert.ok(app.includes('class="panel pulse-summary"'));
  assert.ok(app.includes('class="review-followup"'));
  assert.ok(app.includes('item.id !== memoryID'));
  assert.ok(app.includes('shell("Board", `<div class="home-view board-view">'));
  assert.ok(app.includes('`, { wide: true }))'));
  assert.match(styles, /\.review-grid \{\s+display: grid;\s+grid-template-columns: repeat\(2, minmax\(0, 1fr\)\);/);
  assert.match(styles, /\.board-grid \{[\s\S]*?grid-auto-flow: column;[\s\S]*?grid-auto-columns: minmax\(280px, 320px\);/);
  assert.match(styles, /\.home-pulse-columns \{\s+display: grid;\s+grid-template-columns: minmax\(0, 1\.35fr\) minmax\(280px, 0\.65fr\);/);
  assert.match(styles, /@media \(min-width: 1760px\) \{[\s\S]*?\.board-scroller \{[\s\S]*?overflow-x: visible;[\s\S]*?\.board-grid \{[\s\S]*?grid-template-columns: repeat\(5, minmax\(0, 1fr\)\);/);
  assert.match(styles, /@media \(max-width: 760px\) \{[\s\S]*?\.home-view > \.view-tabs a \{[\s\S]*?min-height: 44px;[\s\S]*?\.home-pulse-primary,[\s\S]*?display: contents;[\s\S]*?\.pulse-summary \{[\s\S]*?order: 2;/);
});

test("uses additive knowledge APIs and approachable object fields", () => {
  assert.ok(app.includes('/library/items?'));
  assert.ok(app.includes('request.set("scope", "content")'));
  assert.ok(app.includes('href="/library?scope=derived"'));
  assert.ok(app.includes("Concepts &amp; entities"));
  assert.ok(app.includes('/knowledge-graph/v2?'));
  assert.ok(app.includes('insightQuery.set("family", family)'));
  assert.ok(app.includes('api(`/insights?${insightQuery}`)'));
  assert.ok(app.includes('state === "not_enough_history"'));
  assert.ok(app.includes('state === "reprocessing_required"'));
  assert.ok(app.includes('insight.kind === "recommendation"'));
  assert.ok(app.includes('target_type: "relationship"'));
  assert.ok(app.includes('target_type: "insight"'));
  assert.ok(app.includes('target_type: "insight_impression"'));
  assert.ok(app.includes('cursor: button.dataset.cursor'));
  assert.ok(app.includes("result.restart_required"));
  assert.ok(app.includes('data-insight-reason'));
  assert.ok(!app.includes("Fields JSON"));
  assert.ok(!app.includes("Fields JSON must be an object."));
});

test("keeps administration failure logs readable and supports bounded bulk retry", () => {
  for (const contract of [
    'class="table-wrap admin-failures-wrap"',
    "data-admin-select-all-jobs",
    "data-admin-retry-selected",
    "data-admin-retry-user",
    'api("/admin/jobs/retry"',
    "Retry failures (",
  ]) assert.ok(app.includes(contract), `missing admin retry contract: ${contract}`);
  assert.ok(styles.includes(".admin-failure-attempts"));
  assert.ok(styles.includes(".admin-action-cell button"));
  assert.ok(styles.includes("white-space: nowrap"));
});

test("provides discoverable global keyboard shortcuts without hijacking form input", () => {
  for (const contract of [
    "function globalKeyboardShortcuts(event)",
    "function openKeyboardShortcuts()",
    "function isShortcutTypingTarget(target)",
    'data-command-shortcuts>Keyboard shortcuts',
    'event.key === "?"',
    'key === "q"',
    'event.key === "/" || key === "f"',
    'event.key.toLowerCase() === "k"',
    'key === "p"',
    'event.key === "ArrowDown" || event.key === "ArrowUp"',
    "document.querySelector(\".dialog-backdrop\")",
  ]) assert.ok(app.includes(contract), `missing keyboard shortcut contract: ${contract}`);
  assert.ok(app.includes('data-shortcut-item href="/bookmark/'));
  assert.ok(app.includes('data-shortcut-item>'));
  assert.ok(styles.includes(".shortcut-row kbd"));
  assert.ok(styles.includes("[data-shortcut-item].keyboard-selected"));
});

test("shows restrained shortcut hints on persistent actions", () => {
  for (const contract of [
    'id="global-capture" type="button" aria-keyshortcuts="Q"',
    'href="/search" aria-keyshortcuts="/"',
    'aria-keyshortcuts="Meta+K Control+K"',
    'class="shortcut-badge" aria-hidden="true"',
  ]) assert.ok(app.includes(contract), `missing shortcut hint contract: ${contract}`);
  assert.ok(styles.includes(".shortcut-badge"));
  assert.match(styles, /@media \(max-width: 760px\) \{[\s\S]*?\.top-actions \.shortcut-badge \{\s+display: none;/);
});

test("escapes user-controlled library filters in pagination links", () => {
  assert.ok(app.includes("escapeHTML(libraryNextParams(params, result.next_cursor))"));
});

test("ships a responsive light-only theme and accessible graph fallbacks", () => {
  for (const contract of [
    "color-scheme: light",
	"/fonts/geist-variable.woff2",
	"/fonts/noto-serif-variable-latin.woff2",
    ".mobile-nav",
    ".graph-list",
    "@media (prefers-reduced-motion: reduce)",
  ]) assert.ok(styles.includes(contract), `missing style contract: ${contract}`);
  assert.ok(!styles.includes("prefers-color-scheme: dark"));
  assert.ok(app.includes('aria-label="Interactive knowledge graph"'));
  assert.ok(app.includes("Accessible node list"));
  assert.ok(app.includes("data-graph-zoom"));
  assert.ok(app.includes('id="global-capture"'));
  assert.ok(app.includes('href="/search"'));
});

test("opens visual graph nodes at their saved knowledge item", () => {
  assert.ok(app.includes('href="${escapeHTML(knowledgeItemHref(node.type, node.source_id, node.title))}"'));
  assert.ok(app.includes('<li><a class="graph-list-node" href="${escapeHTML(knowledgeItemHref(node.type, node.source_id, node.title))}"'));
  assert.ok(!app.includes('<li><button type="button" class="graph-list-node"'));
  assert.ok(styles.includes(".graph-list-node:hover"));
});

test("uses the backend collection filter contract and exposes collection management", () => {
  assert.ok(app.includes('request.set("collection_id", request.get("collection"))'));
  assert.ok(app.includes('href="/library?collection_id=${encodeURIComponent(item.id)}"'));
  assert.ok(app.includes('["collections", "Collections"'));
  assert.ok(app.includes('method: "PATCH", body: JSON.stringify({ name:'));
  assert.ok(app.includes('method: "DELETE" }'));
});

test("keeps imports inside Settings and uses legible interactive states", () => {
  assert.ok(!app.includes('{ label: "Imports and exports", action:'));
  assert.ok(app.includes('{ label: "Settings", action: () => navigate("/settings") }'));
  assert.ok(app.includes('["import", "Import", "Bring in browser, Pocket, Raindrop, or URL-list exports."]'));
  assert.ok(styles.includes('background: var(--accent-hover);\n    color: var(--accent-ink);'));
  assert.ok(styles.includes('--interactive-hover-bg: var(--accent-50);'));
  assert.ok(styles.includes('--interactive-hover-ink: var(--accent-800);'));
  assert.match(styles, /\.tab-list \[role="tab"\]:hover:not\(\[aria-selected="true"\]\) \{\s+background: var\(--interactive-hover-bg\);\s+color: var\(--interactive-hover-ink\);/);
  assert.match(styles, /\.menu \[role="menuitem"\]:hover \{\s+background: var\(--interactive-hover-bg\);\s+color: var\(--interactive-hover-ink\);/);
  assert.match(styles, /summary:hover \{\s+border-radius: var\(--radius\);\s+background: var\(--interactive-hover-bg\);\s+color: var\(--interactive-hover-ink\);/);
  for (const selector of [".bookmark:hover", ".graph-list-node:hover", ".evidence-list a:hover"]) {
    assert.match(styles, new RegExp(`${selector.replaceAll(".", "\\.")} \\{\\s+background: var\\(--interactive-hover-bg\\);\\s+color: var\\(--interactive-hover-ink\\);`));
  }
  assert.ok(styles.includes("button.secondary:hover:not(:disabled)"));
  assert.match(styles, /\.chips a:hover,[\s\S]*?\.collection-tree a:hover \{\s+background: var\(--interactive-hover-bg\);\s+color: var\(--interactive-hover-ink\);/);
  assert.ok(styles.includes(".graph-nodes a:focus-visible .graph-node circle"));
  assert.ok(styles.includes("--placeholder: var(--base-600);"));
  assert.ok(styles.includes("--accent: var(--accent-700);"));
  assert.ok(styles.includes("background: var(--sand-200);\n  color: var(--sand-950);"));
});

test("library, graph, and insights keep accessible composition", () => {
  assert.ok(app.includes('<summary>More filters</summary>'));
  assert.ok(app.includes("if (!(collections || []).length && !selected) return \"\""));
  assert.ok(app.includes('Clear filters'));
  assert.ok(app.includes('class="graph-hit"'));
  assert.ok(app.includes('data-insight-next="capture-note"'));
  assert.ok(styles.includes(".graph-node .graph-hit"));
});

test("public shares use the product typography and labeled controls", () => {
  assert.ok(shareHandlers.includes('<label for=q>Search shared knowledge</label>'));
  assert.ok(shareHandlers.includes('<label for=sort>Sort items</label>'));
  assert.ok(publicShare.includes("resultCount.textContent"));
  assert.ok(publicShareStyles.includes('/fonts/geist-variable.woff2'));
  assert.ok(publicShareStyles.includes('--accent-soft:'));
});
