import assert from "node:assert/strict";
import test from "node:test";

import { registerServiceWorker } from "../web/service-worker-register.mjs";

function browserScope(serviceWorker) {
  let loadListener;
  let loaded = false;
  return {
    navigator: serviceWorker ? { serviceWorker } : {},
    addEventListener(type, listener, options) {
      assert.equal(type, "load");
      assert.deepEqual(options, { once: true });
      loadListener = listener;
    },
    dispatchLoad() {
      if (!loadListener || loaded) return undefined;
      loaded = true;
      return loadListener();
    },
  };
}

test("registers the service worker once after load", async () => {
  const paths = [];
  const scope = browserScope({ register: async (path) => { paths.push(path); } });

  registerServiceWorker(scope);
  await scope.dispatchLoad();
  await scope.dispatchLoad();

  assert.deepEqual(paths, ["/sw.js"]);
});

test("does nothing when service workers are unavailable", () => {
  const scope = browserScope();

  registerServiceWorker(scope);

  assert.equal(scope.dispatchLoad(), undefined);
});

test("contains registration failures", async () => {
  const scope = browserScope({ register: async () => { throw new Error("blocked"); } });

  registerServiceWorker(scope);

  await assert.doesNotReject(scope.dispatchLoad());
});
