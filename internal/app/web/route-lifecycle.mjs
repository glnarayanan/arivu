export class StaleRouteError extends Error {
  constructor() {
    super("stale route");
    this.name = "StaleRouteError";
  }
}

export function createRouteLifecycle({ AbortController: AbortControllerImpl = globalThis.AbortController } = {}) {
  let generation = 0;
  let latest = null;
  let committed = null;

  function begin() {
    latest?.controller.abort();
    const scope = {
      generation: ++generation,
      controller: new AbortControllerImpl(),
      cleanup: [],
      disposed: false,
      get signal() { return this.controller.signal; },
      isCurrent() { return latest === this && !this.signal.aborted; },
      assertCurrent() {
        if (!this.isCurrent()) throw new StaleRouteError();
      },
      addCleanup(cleanup) {
        if (this.disposed) cleanup();
        else this.cleanup.push(cleanup);
      },
      dispose() {
        if (this.disposed) return;
        this.disposed = true;
        while (this.cleanup.length) this.cleanup.pop()();
      },
      commit(mutate) {
        this.assertCurrent();
        if (committed !== this) {
          committed?.dispose();
          committed = this;
        }
        mutate();
      },
    };
    latest = scope;
    return scope;
  }

  return {
    begin,
    addCleanup(cleanup) {
      if (!committed) throw new Error("route cleanup registered before commit");
      committed.addCleanup(cleanup);
    },
    isStale(error) { return error instanceof StaleRouteError; },
  };
}
