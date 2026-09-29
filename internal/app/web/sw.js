const CACHE = "arivu-shell-v5";
const SHELL = ["/", "/today", "/library", "/notes", "/graph", "/insights", "/search", "/dashboard", "/app.js", "/route-lifecycle.mjs", "/service-worker-register.mjs", "/styles.css", "/fonts/geist-variable.woff2", "/fonts/geist-mono-variable.woff2", "/fonts/noto-serif-variable-latin.woff2", "/favicon.svg", "/manifest.webmanifest"];

self.addEventListener("install", (event) => {
  event.waitUntil(caches.open(CACHE).then((cache) => cache.addAll(SHELL)));
  self.skipWaiting();
});

self.addEventListener("activate", (event) => {
  event.waitUntil(caches.keys().then((keys) => Promise.all(keys.filter((key) => key !== CACHE).map((key) => caches.delete(key)))));
  self.clients.claim();
});

self.addEventListener("fetch", (event) => {
  const request = event.request;
  const url = new URL(request.url);
  if (request.method !== "GET" || url.origin !== location.origin || url.pathname.startsWith("/api/")) return;
  event.respondWith(fetch(request, { cache: "no-cache" }).catch(() => caches.match(request).then((cached) => cached || caches.match("/"))));
});
