export function registerServiceWorker(scope = globalThis) {
  const serviceWorker = scope.navigator?.serviceWorker;
  if (!serviceWorker) return;
  scope.addEventListener("load", () => serviceWorker.register("/sw.js").catch(() => {}), { once: true });
}
