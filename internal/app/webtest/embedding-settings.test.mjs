import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

const app = await readFile(new URL("../web/app.js", import.meta.url), "utf8");

// Extract a top-level function declaration from app.js so its behaviour can be
// exercised without a browser.
function extractFunction(name) {
  const start = app.indexOf(`function ${name}(`);
  assert.ok(start >= 0, `missing function ${name}`);
  let depth = 0;
  for (let index = app.indexOf("{", start); index < app.length; index += 1) {
    if (app[index] === "{") depth += 1;
    if (app[index] === "}") depth -= 1;
    if (depth === 0) return app.slice(start, index + 1);
  }
  throw new Error(`unterminated function ${name}`);
}

const escapeHTML = (value) => String(value ?? "").replace(/[&<>"']/g, (char) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[char]);
const formatCount = (value) => Number(value || 0).toLocaleString("en-US");
const load = (...names) => new Function("escapeHTML", "formatCount", `${names.map(extractFunction).join("\n")}; return { ${names.join(", ")} };`)(escapeHTML, formatCount);

const presets = [
  { id: "openai", name: "OpenAI", supports_embeddings: true },
  { id: "deepseek", name: "DeepSeek", supports_embeddings: false, note: "DeepSeek does not offer an embeddings API." },
];

test("labels text generation and embedding settings separately in both admin forms", () => {
  for (const contract of [
    "<legend>Text generation model</legend>",
    "<legend>Embedding model</legend>",
    'label for="${prefix}ai-provider">Text generation provider</label>',
    'label for="${prefix}embedding-provider">Embedding provider</label>',
    'label for="${prefix}embedding-api-key">Embedding API key</label>',
    'label for="${prefix}embedding-dimensions">Embedding dimensions</label>',
    '${textModelFieldset("")}',
    '${embeddingFieldset("")}',
    '${textModelFieldset("admin-", selectedProvider, settings)}',
    '${embeddingFieldset("admin-")}',
    'collectEmbeddingSettings(form, "", body);',
    'collectEmbeddingSettings(form, "admin-", body);',
    'api("/admin/embeddings")',
    'api("/admin/embeddings/reembed", { method: "POST", body: "{}" })',
  ]) assert.ok(app.includes(contract), `missing embedding settings contract: ${contract}`);
  assert.ok(!app.includes('<label for="admin-ai-provider">Model Provider</label>'), "ambiguous provider label remains");
  assert.ok(!app.includes('ai_provider: "Model Provider"'), "ambiguous status label remains");
});

test("offers same-as-text, embedding-capable presets, and disabled text-only presets", () => {
  const { embeddingProviderOptions } = load("embeddingProviderOptions");
  const html = embeddingProviderOptions(presets, "openai");
  assert.match(html, /<option value="same">Same as text generation<\/option>/);
  assert.match(html, /<optgroup label="Embedding providers"><option value="openai" selected>OpenAI<\/option><\/optgroup>/);
  assert.match(html, /<option value="deepseek" disabled>DeepSeek \(text only\)<\/option>/);
  assert.match(html, /<option value="none">Off<\/option>/);
});

test("explains unconfigured embeddings and the affected features", () => {
  const { embeddingStatusHTML } = load("embeddingStatusHTML");
  const html = embeddingStatusHTML({
    configured: false,
    message: "The text generation provider (DeepSeek) has no embeddings API, so an embedding model must be configured separately.",
    text_provider_name: "DeepSeek",
    text_provider_supports_embeddings: false,
    affected_features: ["Related items on a bookmark", "Similarity links in the Graph"],
    vectors: {},
  });
  assert.match(html, /Embeddings are not configured\./);
  assert.match(html, /The text generation provider \(DeepSeek\) has no embeddings API/);
  assert.match(html, /<li>Related items on a bookmark<\/li><li>Similarity links in the Graph<\/li>/);
  assert.doesNotMatch(html, /data-embedding-reembed/);
});

test("shows re-embedding progress and offers a manual pass only when idle", () => {
  const { embeddingStatusHTML } = load("embeddingStatusHTML");
  const busy = embeddingStatusHTML({ configured: true, mode: "same", provider_name: "OpenRouter", model: "openai/text-embedding-3-small", dimensions: 0, vectors: { current: 3, stale: 2, missing: 1, job_queued: true } });
  assert.match(busy, /Embedding model: OpenRouter · openai\/text-embedding-3-small<\/strong>/);
  assert.match(busy, /3 embedded · 2 from a previous model · 1 not embedded yet\. Re-embedding in the background\./);
  assert.doesNotMatch(busy, /data-embedding-reembed/);
  const idle = embeddingStatusHTML({ configured: true, mode: "separate", provider_name: "OpenAI", model: "text-embedding-3-large", dimensions: 1024, vectors: { current: 1, stale: 4, missing: 0 } });
  assert.match(idle, /1,024 dimensions/);
  assert.match(idle, /data-embedding-reembed/);
});

test("submits embedding settings without leaking keys across modes", () => {
  const { collectEmbeddingSettings } = load("collectEmbeddingSettings");
  const fields = (values) => ({
    querySelector(selector) {
      const key = selector.replace(/^#(admin-)?/, "");
      return key in values ? { value: values[key] } : null;
    },
  });
  const same = {};
  collectEmbeddingSettings(fields({ "embedding-provider": "same", "embedding-model": " ", "embedding-dimensions": "", "embedding-base-url": "https://ignored.example", "embedding-api-key": "sk-should-not-send" }), "", same);
  assert.deepEqual(same, { embedding_provider: "same", embedding_model: "", embedding_dimensions: "", embedding_base_url: "" });
  const separate = {};
  collectEmbeddingSettings(fields({ "embedding-provider": "openai", "embedding-model": "text-embedding-3-large", "embedding-dimensions": "1024", "embedding-base-url": "", "embedding-api-key": "sk-new" }), "admin-", separate);
  assert.deepEqual(separate, { embedding_provider: "openai", embedding_model: "text-embedding-3-large", embedding_dimensions: "1024", embedding_base_url: "", embedding_api_key: "sk-new" });
  const off = {};
  collectEmbeddingSettings(fields({ "embedding-provider": "none" }), "", off);
  assert.deepEqual(off, { embedding_provider: "none" });
});

test("surfaces semantic status on Graph and related items", () => {
  assert.ok(app.includes("graph.semantic_message ?"));
  assert.ok(app.includes('relatedList(related.related || [], related.semantic_status || "")'));
  assert.ok(app.includes('adminStat("Embedding model"'));
});
