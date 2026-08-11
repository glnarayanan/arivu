import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

import { createRouteLifecycle, StaleRouteError } from "../web/route-lifecycle.mjs";

test("a slow route cannot commit after a newer route", () => {
  const lifecycle = createRouteLifecycle();
  const dom = { page: "initial" };
  const slow = lifecycle.begin();
  const current = lifecycle.begin();

  current.commit(() => { dom.page = "current"; });
  assert.throws(() => slow.commit(() => { dom.page = "slow"; }), StaleRouteError);

  assert.equal(dom.page, "current");
  assert.equal(slow.signal.aborted, true);
});

test("a stale post-commit async continuation is rejected before mutation", async () => {
  const lifecycle = createRouteLifecycle();
  const dom = { page: "settings", detail: "loading" };
  const settings = lifecycle.begin();
  settings.commit(() => { dom.page = "settings"; });
  let release;
  const response = new Promise((resolve) => { release = resolve; });
  const continuation = (async () => {
    const detail = await response;
    settings.commit(() => { dom.detail = detail; });
  })();

  lifecycle.begin().commit(() => { dom.page = "library"; dom.detail = "current"; });
  release("stale settings");

  await assert.rejects(continuation, StaleRouteError);
  assert.deepEqual(dom, { page: "library", detail: "current" });
});

test("a stale route cannot dispose the current route listeners", () => {
  const lifecycle = createRouteLifecycle();
  const disposed = [];
  const first = lifecycle.begin();
  first.commit(() => {});
  first.addCleanup(() => disposed.push("first"));

  const second = lifecycle.begin();
  second.commit(() => {});
  second.addCleanup(() => disposed.push("second"));
  assert.deepEqual(disposed, ["first"]);

  assert.throws(() => first.commit(() => {}), StaleRouteError);
  assert.deepEqual(disposed, ["first"]);

  const third = lifecycle.begin();
  third.commit(() => {});
  assert.deepEqual(disposed, ["first", "second"]);
});

test("route cleanup is LIFO and runs once", () => {
  const lifecycle = createRouteLifecycle();
  const calls = [];
  const route = lifecycle.begin();
  route.commit(() => {});
  route.addCleanup(() => calls.push(1));
  route.addCleanup(() => calls.push(2));

  lifecycle.begin().commit(() => {});
  route.dispose();

  assert.deepEqual(calls, [2, 1]);
});

test("a failed successor route replaces the previous committed page", () => {
  const lifecycle = createRouteLifecycle();
  const dom = { page: "library", retry: false };
  const library = lifecycle.begin();
  library.commit(() => { dom.page = "library"; });

  const failed = lifecycle.begin();
  try {
    throw new Error("insights unavailable");
  } catch (err) {
    if (failed.isCurrent() && !lifecycle.isStale(err)) {
      failed.commit(() => { dom.page = "error"; dom.retry = true; });
    }
  }

  assert.deepEqual(dom, { page: "error", retry: true });
  assert.equal(library.signal.aborted, true);
  assert.equal(failed.isCurrent(), true);
});

test("a delayed insight failure cannot toast or restore controls on a successor route", async () => {
  const lifecycle = createRouteLifecycle();
  const insights = lifecycle.begin();
  let rejectRequest;
  const request = new Promise((resolve, reject) => { rejectRequest = reject; });
  const effects = [];
  const handler = (async () => {
    if (!insights.isCurrent()) return;
    try {
      await request;
      insights.assertCurrent();
    } catch (err) {
      if (!insights.isCurrent() || lifecycle.isStale(err)) return;
      effects.push("toast");
    } finally {
      if (insights.isCurrent()) effects.push("restore button");
    }
  })();

  lifecycle.begin();
  rejectRequest(new Error("insights unavailable"));
  await handler;

  assert.deepEqual(effects, []);
});

test("route-owned destructive and admin binders carry their scope", async () => {
  const source = await readFile(new URL("../web/app.js", import.meta.url), "utf8");

  assert.match(source, /deleteStandaloneNote\(scope, button\)/);
  assert.match(source, /deleteStandaloneNote\(scope, button\)[\s\S]*?confirmDestructive[\s\S]*?scope\.assertCurrent\(\)[\s\S]*?await api[\s\S]*?scope\.assertCurrent\(\)/);
  assert.match(source, /Delete bookmark[\s\S]*?scope\.assertCurrent\(\)[\s\S]*?await api\(`\/bookmarks\/\$\{id\}`[\s\S]*?scope\.assertCurrent\(\)/);
  assert.match(source, /bindAdminSettingsPanel\(scope\)/);
  assert.match(source, /function bindAdminSettingsPanel\(scope\)[\s\S]*?const settings = await api\("\/admin\/settings"\);\s*scope\.assertCurrent\(\)/);
  assert.match(source, /Delete collection[\s\S]*?if \(!confirmed\) return;\s*scope\.assertCurrent\(\)/);
  assert.match(source, /Disconnect X[\s\S]*?if \(!confirmed\) return;\s*scope\.assertCurrent\(\)/);
});

test("annotation deletion does not submit after its delayed confirmation becomes stale", async () => {
  const lifecycle = createRouteLifecycle();
  const annotationRoute = lifecycle.begin();
  let confirmDelete;
  const confirmation = new Promise((resolve) => { confirmDelete = resolve; });
  const requests = [];
  const deletion = (async () => {
    if (!await confirmation) return;
    annotationRoute.assertCurrent();
    requests.push("DELETE /annotations/annotation-1");
  })();

  lifecycle.begin();
  confirmDelete(true);

  await assert.rejects(deletion, StaleRouteError);
  assert.deepEqual(requests, []);
});

test("a delayed admin retry failure is silently ignored by a stale event catch", async () => {
  const lifecycle = createRouteLifecycle();
  const adminRoute = lifecycle.begin();
  let rejectRetry;
  const retry = new Promise((resolve, reject) => { rejectRetry = reject; });
  const toasts = [];
  const handler = (async () => {
    try {
      await retry;
      adminRoute.assertCurrent();
    } catch (err) {
      if (!adminRoute.signal.aborted && !lifecycle.isStale(err)) toasts.push(err.message);
    }
  })();

  lifecycle.begin();
  rejectRetry(new Error("retry failed after navigation"));
  await handler;

  assert.deepEqual(toasts, []);
});

test("a delayed password reset cannot mutate a successor route", async () => {
  const lifecycle = createRouteLifecycle();
  const reset = lifecycle.begin();
  let releaseRequest;
  const request = new Promise((resolve) => { releaseRequest = resolve; });
  const effects = [];
  const submission = (async () => {
    if (!reset.isCurrent()) return;
    try {
      await request;
      reset.assertCurrent();
      effects.push("message", "toast", "hide submit");
    } catch (err) {
      if (!reset.isCurrent() || lifecycle.isStale(err)) return;
      effects.push("error");
    } finally {
      if (reset.isCurrent()) effects.push("restore button");
    }
  })();

  lifecycle.begin();
  releaseRequest();
  await submission;
  assert.deepEqual(effects, []);
});

test("delayed capture processing cannot navigate after leaving the capture route", async () => {
  const lifecycle = createRouteLifecycle();
  const capture = lifecycle.begin();
  let releasePoll;
  const poll = new Promise((resolve) => { releasePoll = resolve; });
  const navigations = [];
  const submission = (async () => {
    try {
      capture.assertCurrent();
      await Promise.resolve({ bookmark: { id: "bookmark-1" }, job_id: "job-1" });
      capture.assertCurrent();
      await poll;
      capture.assertCurrent();
      navigations.push("/bookmark/bookmark-1");
    } catch (err) {
      if (!capture.isCurrent() || lifecycle.isStale(err)) return;
      throw err;
    }
  })();

  lifecycle.begin();
  releasePoll({ status: "completed" });
  await submission;

  assert.deepEqual(navigations, []);
});

test("delayed Assistant action cannot navigate, toast, or restore controls after navigation", async () => {
  const lifecycle = createRouteLifecycle();
  const assistant = lifecycle.begin();
  let releaseAction;
  const action = new Promise((resolve) => { releaseAction = resolve; });
  const effects = [];
  const submission = (async () => {
    assistant.assertCurrent();
    try {
      await action;
      assistant.assertCurrent();
      effects.push("toast", "navigate");
    } catch (err) {
      if (!assistant.isCurrent() || lifecycle.isStale(err)) return;
      effects.push("error toast");
    } finally {
      if (assistant.isCurrent()) effects.push("restore button");
    }
  })();

  lifecycle.begin();
  releaseAction();
  await submission;

  assert.deepEqual(effects, []);
});

test("reader composer delayed open and submission cannot outlive its route", async () => {
  const lifecycle = createRouteLifecycle();
  const reader = lifecycle.begin();
  let releaseFrame;
  const frame = new Promise((resolve) => { releaseFrame = resolve; });
  const composers = [];
  const scheduledOpen = (async () => {
    await frame;
    if (reader.isCurrent()) composers.push("composer");
  })();

  lifecycle.begin();
  releaseFrame();
  await scheduledOpen;
  assert.deepEqual(composers, []);

  const secondReader = lifecycle.begin();
  let releaseSave;
  const save = new Promise((resolve) => { releaseSave = resolve; });
  const mutations = [];
  const submission = (async () => {
    secondReader.assertCurrent();
    try {
      await save;
      secondReader.assertCurrent();
      mutations.push("close composer", "toast", "render");
    } catch (err) {
      if (!secondReader.isCurrent() || lifecycle.isStale(err)) return;
      throw err;
    }
  })();
  lifecycle.begin();
  releaseSave();
  await submission;
  assert.deepEqual(mutations, []);
});
