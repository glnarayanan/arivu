import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

const app = await readFile(new URL("../web/app.js", import.meta.url), "utf8");
const styles = await readFile(new URL("../web/styles.css", import.meta.url), "utf8");

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
});

test("uses additive knowledge APIs and approachable object fields", () => {
  assert.ok(app.includes('/library/items?'));
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
