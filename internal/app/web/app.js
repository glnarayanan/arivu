import { registerServiceWorker } from "/service-worker-register.mjs";

const state = {
  user: null,
  cleanup: [],
  pendingRoutes: 0,
  focusMainAfterRender: false,
};
const offlineQueueKey = "arivu.offline.bookmarks";
const offlineSnapshotKey = "arivu.offline.snapshots";
const modelProviderPresets = [
  { id: "openai", label: "OpenAI", baseURL: "https://api.openai.com/v1", defaultModel: "" },
  { id: "openrouter", label: "OpenRouter", baseURL: "https://openrouter.ai/api/v1", defaultModel: "~openai/gpt-latest" },
  { id: "xai", label: "xAI", baseURL: "https://api.x.ai/v1", defaultModel: "grok-4.5" },
  { id: "gemini", label: "Gemini", baseURL: "https://generativelanguage.googleapis.com", defaultModel: "gemini-2.5-flash" },
  { id: "anthropic", label: "Anthropic", baseURL: "https://api.anthropic.com", defaultModel: "" },
  { id: "deepseek", label: "DeepSeek", baseURL: "https://api.deepseek.com", defaultModel: "deepseek-v4-pro" },
  { id: "mistral", label: "Mistral", baseURL: "https://api.mistral.ai/v1", defaultModel: "mistral-large-latest" },
  { id: "groq", label: "Groq", baseURL: "https://api.groq.com/openai/v1", defaultModel: "" },
  { id: "together", label: "Together AI", baseURL: "https://api.together.ai/v1", defaultModel: "" },
  { id: "fireworks", label: "Fireworks AI", baseURL: "https://api.fireworks.ai/inference/v1", defaultModel: "" },
  { id: "perplexity", label: "Perplexity", baseURL: "https://api.perplexity.ai", defaultModel: "sonar-pro" },
  { id: "cerebras", label: "Cerebras", baseURL: "https://api.cerebras.ai/v1", defaultModel: "" },
  { id: "zai", label: "Z.ai", baseURL: "https://api.z.ai/api/paas/v4", defaultModel: "glm-4.5" },
  { id: "huggingface", label: "Hugging Face", baseURL: "https://router.huggingface.co/v1", defaultModel: "" },
  { id: "lmstudio", label: "LM Studio", baseURL: "http://localhost:1234/v1", defaultModel: "" },
  { id: "ollama", label: "Ollama/local", baseURL: "http://localhost:11434/v1", defaultModel: "" },
  { id: "minimax", label: "MiniMax", baseURL: "https://api.minimax.io/v1", defaultModel: "" },
  { id: "custom", label: "Custom", baseURL: "", defaultModel: "" },
];

const routes = [
  { prefix: "/auth", page: authPage, access: "public" },
  { prefix: "/reset-password", page: resetPasswordPage, access: "public" },
  { prefix: "/accept-invite", page: acceptInvitePage, access: "public" },
  { prefix: "/today", page: todayPage, access: "protected" },
  { prefix: "/library", page: libraryPage, access: "protected" },
  { prefix: "/graph", page: graphPage, access: "protected" },
  { prefix: "/insights", page: insightsPage, access: "protected" },
  { prefix: "/search", page: searchPage, access: "protected" },
  { prefix: "/dashboard", page: () => compatibilityRedirect("/library", { view: "capture" }), access: "protected" },
  { prefix: "/bookmark/", page: bookmarkPage, access: "protected" },
  { prefix: "/inbox", page: () => compatibilityRedirect("/library", { view: "inbox", stage: "inbox" }), access: "protected" },
  { prefix: "/focus", page: focusCompatibilityRedirect, access: "protected" },
  { prefix: "/assistant", page: () => compatibilityRedirect("/search", { mode: "ask", review: "actions" }), access: "protected" },
  { prefix: "/notes/", page: notesPage, access: "protected" },
  { prefix: "/notes", page: notesPage, access: "protected" },
  { prefix: "/objects", page: () => compatibilityRedirect("/library", { type: "knowledge_object" }), access: "protected" },
  { prefix: "/evolution", page: () => compatibilityRedirect("/insights", { family: "changed_thinking", legacy: "evolution" }), access: "protected" },
  { prefix: "/board", page: () => homeViewRedirect("board"), access: "protected" },
  { prefix: "/review", page: () => homeViewRedirect("review"), access: "protected" },
  { prefix: "/duplicates", page: () => compatibilityRedirect("/library", { management: "duplicates" }), access: "protected" },
  { prefix: "/settings", page: settingsPage, access: "protected" },
  { prefix: "/imports", page: () => navigate("/settings?section=import", true), access: "protected" },
  { prefix: "/knowledge-graph", page: () => compatibilityRedirect("/graph"), access: "protected" },
  { prefix: "/analytics", page: () => compatibilityRedirect("/insights"), access: "protected" },
  { prefix: "/admin", page: adminPage, access: "protected" },
];

function compatibilityRedirect(path, defaults = {}) {
  const params = new URLSearchParams(location.search);
  Object.entries(defaults).forEach(([key, value]) => {
    if (!params.has(key)) params.set(key, value);
  });
  navigate(`${path}${params.size ? `?${params}` : ""}`, true);
}

function homeViewRedirect(view) {
  const params = new URLSearchParams(location.search);
  params.set("view", view);
  navigate(`/today?${params}`, true);
}

function focusCompatibilityRedirect() {
  const params = new URLSearchParams(location.search);
  const legacyFilter = params.get("view");
  params.set("view", "focus");
  if (["pending", "overdue", "today", "upcoming", "completed"].includes(legacyFilter)) params.set("focus", legacyFilter);
  navigate(`/today?${params}`, true);
}

async function api(path, options = {}) {
  const { retried = false, ...requestOptions } = options;
  const headers = new Headers(requestOptions.headers || {});
  const isFormData = typeof FormData !== "undefined" && requestOptions.body instanceof FormData;
  if (requestOptions.body && !isFormData && !headers.has("Content-Type")) headers.set("Content-Type", "application/json");
  const csrf = getCookie("csrf_token");
  if (csrf) headers.set("X-CSRF-Token", csrf);
  let res;
  try {
    res = await fetch(`/api${path}`, { credentials: "include", ...requestOptions, headers });
  } catch {
    const cached = readOfflineSnapshot(path, requestOptions);
    if (cached) {
      announceOfflineSnapshot();
      return cached;
    }
    throw new Error("We couldn't reach Arivu. Check your connection and try again.");
  }
  if (res.status === 401 && path !== "/auth/refresh" && (!path.startsWith("/auth/") || path.startsWith("/auth/x/")) && !retried) {
    let refreshed;
    try {
      refreshed = await fetch("/api/auth/refresh", { method: "POST", credentials: "include", headers: csrf ? { "X-CSRF-Token": csrf } : {} });
    } catch {
      throw new Error("We couldn't refresh your session. Sign in again.");
    }
    if (refreshed.ok) return api(path, { ...requestOptions, retried: true });
    navigate("/auth", true);
    throw new Error("auth required");
  }
  const type = res.headers.get("Content-Type") || "";
  const text = await res.text();
  const data = type.includes("application/json") && text ? JSON.parse(text) : text;
  const detail = typeof data === "object" && data !== null ? data.detail : data;
  if (!res.ok) throw Object.assign(new Error(detail || "Request failed. Try again."), { status: res.status, data });
  writeOfflineSnapshot(path, requestOptions, data);
  return data;
}

function getCookie(name) {
  return document.cookie.split("; ").find((row) => row.startsWith(`${name}=`))?.split("=")[1] || "";
}

function escapeHTML(value) {
  return String(value).replace(/[&<>"']/g, (ch) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[ch]);
}

function setRoot(markup) {
  disposeRoute();
  const root = document.querySelector("#app");
  root.innerHTML = markup;
}

function syncRouteAccessibility() {
  const title = document.querySelector(".headline, main h1, h1")?.textContent?.trim() || "Arivu";
  document.title = `${title} · Arivu`;
  let announcer = document.querySelector("#route-announcer");
  if (!announcer) {
    announcer = document.createElement("div");
    announcer.id = "route-announcer";
    announcer.className = "sr-only";
    announcer.setAttribute("aria-live", "polite");
    announcer.setAttribute("aria-atomic", "true");
    document.body.append(announcer);
  }
  announcer.textContent = title;
  if (!state.focusMainAfterRender) return;
  state.focusMainAfterRender = false;
  const target = document.querySelector("#main-content") || document.querySelector("main") || document.querySelector("h1");
  if (!target) return;
  if (!target.hasAttribute("tabindex")) target.setAttribute("tabindex", "-1");
  requestAnimationFrame(() => target.focus({ preventScroll: true }));
}

function toast(message, tone = "info") {
  const region = document.querySelector("#toast-region");
  const item = document.createElement("div");
  const safeTone = tone === "success" || tone === "error" ? tone : "info";
  item.className = `toast toast-${safeTone}${isEarnedSuccessToast(message, safeTone) ? " toast-earned" : ""}`;
  item.setAttribute("role", safeTone === "error" ? "alert" : "status");
  item.setAttribute("aria-live", safeTone === "error" ? "assertive" : "polite");
  item.textContent = message;
  region.append(item);
  setTimeout(() => item.remove(), 3200);
}

function isEarnedSuccessToast(message, tone) {
  return tone === "success" && /\b(saved|review|import|queued)\b/i.test(message);
}

function setFormMessage(form, message = "", tone = "error") {
  const item = form?.querySelector("[data-form-message]");
  if (!form || !item) return;
  const ids = item.id ? [item.id] : [];
  item.hidden = !message;
  item.textContent = message;
  item.className = `form-message form-message-${tone}`;
  item.setAttribute("role", tone === "error" ? "alert" : "status");
  item.setAttribute("aria-live", tone === "error" ? "assertive" : "polite");
  form.querySelectorAll("input:not([data-skip-form-message]), select:not([data-skip-form-message]), textarea:not([data-skip-form-message])").forEach((field) => {
    const describedBy = (field.getAttribute("aria-describedby") || "").split(/\s+/).filter((id) => id && !ids.includes(id));
    if (message && ids.length) {
      field.setAttribute("aria-describedby", [...describedBy, ...ids].join(" "));
      if (tone === "error") field.setAttribute("aria-invalid", "true");
      else field.removeAttribute("aria-invalid");
    } else {
      if (describedBy.length) field.setAttribute("aria-describedby", describedBy.join(" "));
      else field.removeAttribute("aria-describedby");
      field.removeAttribute("aria-invalid");
    }
  });
}

function setButtonBusy(button, busyLabel) {
  if (!button) return () => {};
  const previousLabel = button.textContent;
  button.disabled = true;
  button.setAttribute("aria-busy", "true");
  button.textContent = busyLabel;
  return () => {
    button.disabled = false;
    button.removeAttribute("aria-busy");
    button.textContent = previousLabel;
  };
}

function offlineBookmarkQueue() {
  try {
    return JSON.parse(localStorage.getItem(offlineQueueKey) || "[]").filter((item) => item?.url);
  } catch {
    return [];
  }
}

function setOfflineBookmarkQueue(items) {
  localStorage.setItem(offlineQueueKey, JSON.stringify(items.slice(0, 50)));
}

function queueOfflineBookmark(payload) {
  setOfflineBookmarkQueue([...offlineBookmarkQueue(), { ...payload, queued_at: new Date().toISOString() }]);
}

function offlineSnapshotAllowed(path, options = {}) {
  const method = (options.method || "GET").toUpperCase();
  if (method !== "GET") return false;
  return [
    /^\/bookmarks($|\?)/,
    /^\/bookmarks\/[^/]+($|\/related|\?)/,
    /^\/notes($|\/|\?)/,
    /^\/daily-notes\//,
    /^\/search\/items($|\?)/,
    /^\/objects($|\/|\?)/,
    /^\/evolution($|\?)/,
    /^\/today-board($|\?)/,
    /^\/review($|\?)/,
    /^\/inbox($|\?)/,
    /^\/action-items($|\?)/,
    /^\/reminders($|\?)/,
    /^\/memory-jogger($|\?)/,
	/^\/library\/items($|\?)/,
	/^\/knowledge-graph\/v2($|\?)/,
	/^\/insights($|\?)/,
  ].some((pattern) => pattern.test(path));
}

function offlineSnapshots() {
  try {
    const parsed = JSON.parse(localStorage.getItem(offlineSnapshotKey) || "{}");
    return parsed && typeof parsed === "object" ? parsed : {};
  } catch {
    return {};
  }
}

function readOfflineSnapshot(path, options = {}) {
  if (!offlineSnapshotAllowed(path, options)) return null;
  return offlineSnapshots()[path]?.data || null;
}

function writeOfflineSnapshot(path, options = {}, data = null) {
  if (!offlineSnapshotAllowed(path, options) || data == null) return;
  const snapshots = offlineSnapshots();
  snapshots[path] = { saved_at: new Date().toISOString(), data };
  const limited = Object.fromEntries(Object.entries(snapshots).sort((a, b) => String(b[1]?.saved_at || "").localeCompare(String(a[1]?.saved_at || ""))).slice(0, 40));
  try {
    localStorage.setItem(offlineSnapshotKey, JSON.stringify(limited));
  } catch {
    try {
      const smallest = Object.fromEntries(Object.entries(limited).slice(0, 12));
      localStorage.setItem(offlineSnapshotKey, JSON.stringify(smallest));
    } catch {}
  }
}

function announceOfflineSnapshot() {
  if (state.offlineSnapshotNoticeShown) return;
  state.offlineSnapshotNoticeShown = true;
  setTimeout(() => ui.toast("Showing a recent offline copy.", "info"), 0);
}

async function flushOfflineBookmarks({ quiet = false } = {}) {
  if (!navigator.onLine || !state.user) return;
  const queue = offlineBookmarkQueue();
  if (!queue.length) return;
  const remaining = [];
  for (const item of queue) {
    try {
      await api("/bookmarks", { method: "POST", body: JSON.stringify({ url: item.url, note: item.note || "", tags: item.tags || [] }) });
    } catch (err) {
      if (err.status && err.status !== 401) continue;
      remaining.push(item);
    }
  }
  setOfflineBookmarkQueue(remaining);
  if (!quiet && queue.length !== remaining.length) ui.toast(`${queue.length - remaining.length} offline captures synced`, "success");
}

function offlineQueueMessage() {
  const count = offlineBookmarkQueue().length;
  return count ? `<p class="meta offline-status">${count} offline capture${count === 1 ? "" : "s"} waiting to sync.</p>` : "";
}

function voiceButton(targetID, label = "note") {
  return `<button type="button" class="secondary voice-button" data-voice-target="${escapeHTML(targetID)}" aria-label="Dictate into ${escapeHTML(label)}">Dictate</button>`;
}

function bindVoiceCapture() {
  document.querySelectorAll("[data-voice-target]").forEach((button) => {
    button.addEventListener("click", () => {
      const SpeechRecognition = window.SpeechRecognition || window.webkitSpeechRecognition;
      const target = document.getElementById(button.dataset.voiceTarget);
      if (!SpeechRecognition || !target) {
        ui.toast("Voice capture is not available in this browser", "error");
        return;
      }
      const done = setButtonBusy(button, "Listening");
      const recognition = new SpeechRecognition();
      recognition.lang = navigator.language || "en-US";
      recognition.interimResults = false;
      recognition.maxAlternatives = 1;
      recognition.onresult = (event) => {
        const transcript = Array.from(event.results).map((result) => result[0]?.transcript || "").join(" ").trim();
        if (!transcript) return;
        target.value = [target.value.trim(), transcript].filter(Boolean).join("\n");
        target.dispatchEvent(new Event("input", { bubbles: true }));
      };
      recognition.onerror = () => ui.toast("Voice capture stopped", "error");
      recognition.onend = done;
      try {
        recognition.start();
      } catch {
        done();
        ui.toast("Voice capture is not available right now", "error");
      }
    });
  });
}

const ui = {
  on(target, type, handler, options) {
    target.addEventListener(type, handler, options);
    state.cleanup.push(() => target.removeEventListener(type, handler, options));
  },
  toast,
  dialog({ title, body, actions = [] }) {
    const previous = document.activeElement;
    const backdrop = document.createElement("div");
    backdrop.className = "dialog-backdrop";
    backdrop.innerHTML = `
      <section class="dialog panel" role="dialog" aria-modal="true" aria-labelledby="dialog-title" tabindex="-1">
        <div class="dialog-head">
          <h2 id="dialog-title">${escapeHTML(title)}</h2>
          <button type="button" class="icon-button" data-dialog-close aria-label="Close dialog"><span aria-hidden="true">X</span></button>
        </div>
        <div class="dialog-body"></div>
        <div class="dialog-actions"></div>
      </section>
    `;
    const dialog = backdrop.querySelector(".dialog");
    backdrop.querySelector(".dialog-body").replaceChildren(typeof body === "string" ? textNode(body) : body);
    const actionBar = backdrop.querySelector(".dialog-actions");
    const close = (value) => {
      cleanup();
      backdrop.remove();
      previous?.focus?.();
      resolver(value);
    };
    let resolver = () => {};
    actions.forEach((action) => {
      const button = document.createElement("button");
      button.type = "button";
      button.textContent = action.label;
      button.className = action.kind === "secondary" ? "secondary" : action.kind === "danger" ? "danger" : "";
      button.addEventListener("click", () => {
        if (action.beforeClose?.() === false) return;
        close(action.value);
      });
      actionBar.append(button);
    });
    const closeButton = backdrop.querySelector("[data-dialog-close]");
    closeButton.addEventListener("click", () => close(false));
    backdrop.addEventListener("mousedown", (event) => {
      if (event.target === backdrop) close(false);
    });
    const cleanup = trapFocus(dialog, () => close(false));
    document.body.append(backdrop);
    requestAnimationFrame(() => firstFocusable(dialog)?.focus() || dialog.focus());
    return new Promise((resolve) => { resolver = resolve; });
  },
  confirmDestructive({ title, body, confirm = "Delete", cancel = "Cancel" }) {
    return ui.dialog({
      title,
      body,
      actions: [
        { label: cancel, value: false, kind: "secondary" },
        { label: confirm, value: true, kind: "danger" },
      ],
    });
  },
  menu(button, items) {
    button.setAttribute("aria-haspopup", "menu");
    button.setAttribute("aria-expanded", "false");
    let menu = null;
    const close = () => {
      menu?.remove();
      menu = null;
      button.setAttribute("aria-expanded", "false");
    };
    const open = () => {
      close();
      menu = document.createElement("div");
      menu.className = "menu";
      menu.setAttribute("role", "menu");
      items.forEach((item, index) => {
        const entry = document.createElement("button");
        entry.type = "button";
        entry.setAttribute("role", "menuitem");
        entry.tabIndex = index === 0 ? 0 : -1;
        entry.textContent = item.label;
        entry.addEventListener("click", () => {
          close();
          item.action();
        });
        menu.append(entry);
      });
      button.after(menu);
      button.setAttribute("aria-expanded", "true");
      menu.querySelector("[role='menuitem']")?.focus();
    };
    ui.on(button, "click", () => (menu ? close() : open()));
    ui.on(document, "click", (event) => {
      if (menu && !menu.contains(event.target) && event.target !== button) close();
    });
    ui.on(document, "keydown", (event) => {
      if (!menu) return;
      if (event.key === "Escape") {
        event.preventDefault();
        close();
        button.focus();
      }
      if (event.key === "ArrowDown" || event.key === "ArrowUp") {
        event.preventDefault();
        moveRoving(menu, event.key === "ArrowDown" ? 1 : -1);
      }
    });
  },
  tabs(root) {
    const tabs = [...root.querySelectorAll("[role='tab']")];
    const panels = [...root.querySelectorAll("[role='tabpanel']")];
    const activate = (tab) => {
      tabs.forEach((item) => {
        const selected = item === tab;
        item.setAttribute("aria-selected", String(selected));
        item.tabIndex = selected ? 0 : -1;
      });
      panels.forEach((panel) => {
        panel.hidden = panel.id !== tab.getAttribute("aria-controls");
      });
    };
    tabs.forEach((tab, index) => {
      tab.tabIndex = index === 0 ? 0 : -1;
      ui.on(tab, "click", () => activate(tab));
      ui.on(tab, "keydown", (event) => {
        if (event.key !== "ArrowRight" && event.key !== "ArrowLeft" && event.key !== "Home" && event.key !== "End") return;
        event.preventDefault();
        const next = tabByKey(tabs, index, event.key);
        activate(next);
        next.focus();
      });
    });
    activate(tabs.find((tab) => tab.getAttribute("aria-selected") === "true") || tabs[0]);
  },
};

function disposeRoute() {
  while (state.cleanup.length) state.cleanup.pop()();
}

function textNode(value) {
  return document.createTextNode(value);
}

function focusables(root) {
  return [...root.querySelectorAll("a[href],button:not([disabled]),input:not([disabled]),select:not([disabled]),textarea:not([disabled]),[tabindex]:not([tabindex='-1'])")]
    .filter((item) => item.offsetParent !== null || item === document.activeElement);
}

function firstFocusable(root) {
  return focusables(root)[0];
}

function trapFocus(root, onEscape) {
  const handler = (event) => {
    if (event.key === "Escape") {
      event.preventDefault();
      onEscape();
      return;
    }
    if (event.key !== "Tab") return;
    const items = focusables(root);
    if (!items.length) return;
    const first = items[0];
    const last = items[items.length - 1];
    if (event.shiftKey && document.activeElement === first) {
      event.preventDefault();
      last.focus();
    } else if (!event.shiftKey && document.activeElement === last) {
      event.preventDefault();
      first.focus();
    }
  };
  document.addEventListener("keydown", handler);
  return () => document.removeEventListener("keydown", handler);
}

function moveRoving(root, delta) {
  const items = [...root.querySelectorAll("[role='menuitem']")];
  const current = Math.max(0, items.indexOf(document.activeElement));
  const next = items[(current + delta + items.length) % items.length];
  items.forEach((item) => { item.tabIndex = item === next ? 0 : -1; });
  next?.focus();
}

function tabByKey(tabs, index, key) {
  if (key === "Home") return tabs[0];
  if (key === "End") return tabs[tabs.length - 1];
  return tabs[(index + (key === "ArrowRight" ? 1 : -1) + tabs.length) % tabs.length];
}

function navigate(path, replace = false) {
  state.focusMainAfterRender = true;
  history[replace ? "replaceState" : "pushState"]({}, "", path);
  render();
}

function primaryNavActive(href) {
  return location.pathname === href || location.pathname.startsWith(`${href}/`) || (href === "/library" && location.pathname.startsWith("/bookmark/"));
}

function shell(title, content, { wide = false } = {}) {
  const nav = [
    ["/today", "Home"],
    ["/library", "Library"],
    ["/notes", "Notes"],
    ["/graph", "Graph"],
    ["/insights", "Insights"],
  ];
  return `
    <a class="skip-link" href="#main-content">Skip to content</a>
    <div class="shell${wide ? " shell-wide" : ""}">
      <aside class="sidebar">
        <a class="brand" href="/today" aria-label="Arivu home">Arivu</a>
        <nav class="nav" aria-label="Primary">
          ${nav.map(([href, label]) => {
            const active = primaryNavActive(href);
            return `<a href="${href}"${active ? ` class="active" aria-current="page"` : ""}>${label}</a>`;
          }).join("")}
        </nav>
      </aside>
      <main class="main" id="main-content" tabindex="-1" aria-labelledby="route-title">
        <div class="topbar">
          <div>
            <p class="workspace-label">Private knowledge workspace</p>
            <h1 class="headline" id="route-title">${escapeHTML(title)}</h1>
          </div>
          <div class="top-actions">
            <button id="global-capture" type="button">Capture</button>
            <a class="button secondary" href="/search">Search / Ask</a>
            <button id="global-actions" class="secondary" type="button">More</button>
            <button id="profile-menu" class="icon-button" type="button" aria-label="Open profile and settings menu">${escapeHTML((state.user?.name || state.user?.email || "A").slice(0, 1).toUpperCase())}</button>
          </div>
        </div>
        ${content}
      </main>
      <nav class="mobile-nav" aria-label="Primary mobile">
        ${nav.map(([href, label]) => {
          const active = primaryNavActive(href);
          return `<a href="${href}"${active ? ` class="active" aria-current="page"` : ""}>${label}</a>`;
        }).join("")}
      </nav>
    </div>
  `;
}

async function authPage() {
  setRoot(`
    <main class="auth">
      <section class="panel">
        <h1>Arivu</h1>
        <p class="meta">Save pages worth remembering, then turn them into summaries, signals, and connections.</p>
        <form class="form" id="login-form">
          <div class="field"><label for="email">Email</label><input id="email" type="email" required autocomplete="username"></div>
          <div class="field"><label for="password">Password</label><input id="password" type="password" required autocomplete="current-password"></div>
          <p class="form-message" id="auth-message" data-form-message hidden></p>
          <button type="submit">Sign in</button>
          <button type="button" class="secondary" id="signup">Create account</button>
          <a class="text-link" href="/reset-password">Forgot password?</a>
        </form>
      </section>
    </main>
  `);
  const form = document.querySelector("#login-form");
  const emailInput = form.querySelector("#email");
  const passwordInput = form.querySelector("#password");
  form.addEventListener("submit", async (event) => {
    event.preventDefault();
    const done = setButtonBusy(event.submitter, "Signing in");
    const body = JSON.stringify({ email: emailInput.value, password: passwordInput.value });
    setFormMessage(form);
    try {
      await api("/auth/login", { method: "POST", body });
      ui.toast("Signed in", "success");
      navigate("/today", true);
    } catch (err) {
      setFormMessage(form, err.message);
      ui.toast(err.message, "error");
    } finally {
      done();
    }
  });
  document.querySelector("#signup").addEventListener("click", async (event) => {
    if (!form.reportValidity()) return;
    const done = setButtonBusy(event.currentTarget, "Creating account");
    setFormMessage(form);
    try {
      await api("/auth/signup", { method: "POST", body: JSON.stringify({ email: emailInput.value, password: passwordInput.value }) });
      ui.toast("Account created", "success");
      navigate("/today", true);
    } catch (err) {
      setFormMessage(form, err.message);
      ui.toast(err.message, "error");
    } finally {
      done();
    }
  });
}

async function resetPasswordPage() {
  const token = new URLSearchParams(location.search).get("token") || "";
  setRoot(`
    <main class="auth">
      <section class="panel">
        <p class="meta">Account recovery</p>
        <h1>Reset password</h1>
        ${token ? resetPasswordForm() : forgotPasswordForm()}
      </section>
    </main>
  `);
  const form = document.querySelector("#reset-form");
  form.addEventListener("submit", async (event) => {
    event.preventDefault();
    const done = setButtonBusy(event.submitter, token ? "Resetting password" : "Sending link");
    setFormMessage(form);
    try {
      const path = token ? "/auth/reset-password" : "/auth/forgot-password";
      const body = token
        ? JSON.stringify({ token, new_password: document.querySelector("#new-password").value })
        : JSON.stringify({ email: document.querySelector("#reset-email").value });
      const result = await api(path, { method: "POST", body });
      setFormMessage(form, result.message || "Request received", "success");
      ui.toast(result.message || "Request received", "success");
      if (token) form.querySelector("button[type='submit']").hidden = true;
    } catch (err) {
      setFormMessage(form, err.message);
      ui.toast(err.message, "error");
    } finally {
      done();
    }
  });
}

function forgotPasswordForm() {
  return `
    <p>Enter your account email and Arivu will send reset instructions if the account exists.</p>
    <form class="form" id="reset-form">
      <div class="field"><label for="reset-email">Email</label><input id="reset-email" type="email" required autocomplete="username"></div>
      <p class="form-message" id="reset-message" data-form-message hidden></p>
      <button type="submit">Send reset link</button>
      <a class="text-link" href="/auth">Back to sign in</a>
    </form>
  `;
}

function resetPasswordForm() {
  return `
    <p>Choose a new password for your Arivu account.</p>
    <form class="form" id="reset-form">
      <label class="sr-only" for="reset-username">Email</label><input class="sr-only" id="reset-username" type="email" autocomplete="username" tabindex="-1" data-skip-form-message>
      <div class="field"><label for="new-password">New password</label><input id="new-password" type="password" required minlength="8" autocomplete="new-password"></div>
      <p class="form-message" id="reset-message" data-form-message hidden></p>
      <button type="submit">Reset password</button>
      <a class="text-link" href="/auth">Back to sign in</a>
    </form>
  `;
}

async function acceptInvitePage() {
  setRoot(`
    <main class="auth">
      <section class="panel">
        <p class="meta">Invited account</p>
        <h1>Accept invite</h1>
        <p>Use the email and temporary password your admin provided. You can change the password after signing in.</p>
        <form class="form" id="invite-form">
          <div class="field"><label for="invite-email">Email</label><input id="invite-email" type="email" required autocomplete="username"></div>
          <div class="field"><label for="invite-password">Temporary password</label><input id="invite-password" type="password" required autocomplete="current-password"></div>
          <p class="form-message" id="invite-message" data-form-message hidden></p>
          <button type="submit">Sign in</button>
          <a class="text-link" href="/reset-password">Need a new password?</a>
        </form>
      </section>
    </main>
  `);
  const form = document.querySelector("#invite-form");
  form.addEventListener("submit", async (event) => {
    event.preventDefault();
    const done = setButtonBusy(event.submitter, "Signing in");
    setFormMessage(form);
    try {
      await api("/auth/login", {
        method: "POST",
        body: JSON.stringify({
          email: document.querySelector("#invite-email").value,
          password: document.querySelector("#invite-password").value,
        }),
      });
      ui.toast("Invite accepted", "success");
      navigate("/today", true);
    } catch (err) {
      setFormMessage(form, err.message);
      ui.toast(err.message, "error");
    } finally {
      done();
    }
  });
}

async function todayPage() {
  await requireUser();
  const view = new URLSearchParams(location.search).get("view");
  if (view === "focus") return focusPage();
  if (view === "review") return reviewPage();
  if (view === "board") return boardPage();
  const date = localDateKey();
  const [daily, inbox, actions, reminders, review, memory, notes] = await Promise.all([
    api(`/daily-notes/${date}`),
    api("/inbox?stage=inbox&limit=6").catch(() => ({ items: [], counts: {} })),
    api("/action-items?status=all").catch(() => ({ action_items: [] })),
    api("/reminders?status=all").catch(() => ({ reminders: [] })),
    api("/review?limit=6").catch(() => ({ items: [] })),
    api("/memory-jogger").catch(() => ({ has_memory: false })),
    api("/notes").catch(() => ({ notes: [] })),
  ]);
  const openActions = (actions.action_items || []).filter((item) => item.status !== "completed").slice(0, 6);
  const dueReminders = (reminders.reminders || []).filter((item) => item.status !== "completed" && ["overdue", "today"].includes(item.due_state)).slice(0, 6);
  const note = daily.daily_note || { body: "" };
  setRoot(shell("Home", `<div class="home-view home-pulse">
    ${homeViewTabs("pulse")}
    <section class="home-pulse-columns">
      <div class="home-pulse-primary stack">
        <form class="panel form pulse-daily" id="daily-note-form">
          <span class="meta">${escapeHTML(date)}</span>
          <h2>Daily note</h2>
          <div class="field"><label for="daily-note-body">Plan, decisions, loose thoughts</label><textarea id="daily-note-body" rows="10" placeholder="What matters today?">${escapeHTML(note.body || "")}</textarea>${voiceButton("daily-note-body", "daily note")}</div>
          <p class="form-message" id="daily-note-message" data-form-message hidden></p>
          <button type="submit">Save daily note</button>
        </form>
        ${todayList("New captures", inbox.items || [], "/library?stage=inbox", todayInboxItem, "pulse-captures")}
        ${todayList("Worth revisiting", review.items || [], "/today?view=review", todayReviewItem, "pulse-revisit")}
      </div>
      <div class="home-pulse-rail stack">
        <section class="panel pulse-summary">
          <span class="meta">Knowledge pulse</span>
          <h2>${Number((inbox.counts || {}).inbox || 0)} new · ${openActions.length + dueReminders.length} active · ${(review.items || []).length} worth revisiting</h2>
          <p>Continue a thread, revisit a useful memory, and notice what your recent material is beginning to connect.</p>
          <div class="chips">
            <a href="/library?view=capture">Capture</a>
            <a href="/library?stage=inbox">Triage</a>
            <a href="/today?view=focus">Continue</a>
            <a href="/today?view=review">Review</a>
          </div>
        </section>
        <div class="pulse-memory">${memoryCard(memory)}</div>
        ${todayList("Continue thinking", [...openActions, ...dueReminders].slice(0, 8), "/today?view=focus", todayWorkItem, "pulse-continue")}
        <section class="panel pulse-fast-capture">
          <h2>Fast capture</h2>
          <p>Save a link, note, quote, or file without deciding where it belongs first.</p>
          <div class="chips">
            <a href="/library?view=capture">Capture</a>
            <a href="/notes">New note</a>
            <a href="/search?mode=ask">Ask Arivu</a>
          </div>
        </section>
        <section class="panel pulse-recent-notes">
          <h2>Recent notes</h2>
          ${todayListBody((notes.notes || []).slice(0, 5), todayNoteItem)}
          <p><a class="text-link" href="/notes">Open notes</a></p>
        </section>
      </div>
    </section>
  </div>`));
  const form = document.querySelector("#daily-note-form");
  bindVoiceCapture();
  form.addEventListener("submit", async (event) => {
    event.preventDefault();
    const done = setButtonBusy(event.submitter, "Saving");
    setFormMessage(form);
    try {
      await api(`/daily-notes/${date}`, { method: "PUT", body: JSON.stringify({ body: document.querySelector("#daily-note-body").value }) });
      setFormMessage(form, "Daily note saved.", "success");
      ui.toast("Daily note saved", "success");
    } catch (err) {
      setFormMessage(form, err.message);
      ui.toast(err.message, "error");
    } finally {
      done();
    }
  });
}

function homeViewTabs(active) {
  const views = [
    ["pulse", "Pulse", "/today"],
    ["focus", "Focus", "/today?view=focus"],
    ["review", "Review", "/today?view=review"],
    ["board", "Board", "/today?view=board"],
  ];
  return `<nav class="view-tabs" aria-label="Home views">${views.map(([id, label, href]) => `<a href="${href}"${id === active ? ` aria-current="page"` : ""}>${label}</a>`).join("")}</nav>`;
}

function localDateKey(value = new Date()) {
  const tzOffset = value.getTimezoneOffset() * 60000;
  return new Date(value.getTime() - tzOffset).toISOString().slice(0, 10);
}

function todayList(title, items, href, renderItem, className = "") {
  return `<section class="panel${className ? ` ${className}` : ""}">
    <h2>${escapeHTML(title)}</h2>
    ${todayListBody(items, renderItem)}
    <p><a class="text-link" href="${href}">Open ${escapeHTML(title.toLowerCase())}</a></p>
  </section>`;
}

function todayListBody(items, renderItem) {
  if (!items.length) return `<p class="meta">Nothing waiting here.</p>`;
  return `<div class="stack">${items.map(renderItem).join("")}</div>`;
}

function todayInboxItem(item) {
  const isNote = item.item_type === "note";
  return `<article class="annotation">
    <p><strong>${escapeHTML(item.title || item.url || "Untitled")}</strong></p>
    <p class="meta">${escapeHTML(stageLabel(item.stage || "inbox"))} · ${escapeHTML(item.next_action || item.domain || item.source || "")}</p>
    <a class="text-link" href="${isNote ? `/notes/${encodeURIComponent(item.id)}` : `/bookmark/${encodeURIComponent(item.id)}`}">Open</a>
  </article>`;
}

function todayWorkItem(item) {
  const isReminder = Boolean(item.due_at);
  return `<article class="annotation">
    <p><strong>${escapeHTML(isReminder ? formatDate(item.due_at) : item.title || "Action item")}</strong></p>
    <p class="meta">${escapeHTML(item.item_title || "")}${isReminder ? ` · ${escapeHTML(item.due_state || "")}` : ""}</p>
    <a class="text-link" href="${itemHref(item)}">Open source</a>
  </article>`;
}

function todayReviewItem(item) {
  const isNote = item.item_type === "note";
  return `<article class="annotation">
    <p><strong>${escapeHTML(item.title || item.url || "Untitled")}</strong></p>
    <p class="meta">${(item.review_reasons || []).slice(0, 2).map(escapeHTML).join(" · ") || escapeHTML(item.resurfacing_reason || "review")}</p>
    <a class="text-link" href="${isNote ? `/notes/${encodeURIComponent(item.id)}` : `/bookmark/${encodeURIComponent(item.id)}`}">Open</a>
  </article>`;
}

function todayNoteItem(note) {
  return `<article class="annotation">
    <p><strong>${escapeHTML(note.title || "Untitled note")}</strong></p>
    <p class="meta">${escapeHTML(formatDate(note.updated_at))}</p>
    <a class="text-link" href="/notes/${encodeURIComponent(note.id)}">Open note</a>
  </article>`;
}

function currentItemRef() {
  const bookmark = location.pathname.match(/^\/bookmark\/([^/]+)/);
  if (bookmark) return { type: "bookmark", id: decodeURIComponent(bookmark[1]) };
  const note = location.pathname.match(/^\/notes\/([^/]+)/);
  if (note) return { type: "note", id: decodeURIComponent(note[1]) };
  return null;
}

async function libraryPage() {
  await requireUser();
  const params = new URLSearchParams(location.search);
  if (params.get("management") === "duplicates") return duplicatesPage();
  if (params.get("view") === "capture") return dashboardPage();
  if (params.get("view") === "inbox") return inboxPage();
  const request = new URLSearchParams(params);
  request.delete("view");
  request.delete("management");
  if (request.has("collection") && !request.has("collection_id")) request.set("collection_id", request.get("collection"));
  request.delete("collection");
  if (request.has("search") && !request.has("q")) request.set("q", request.get("search"));
  request.delete("search");
  if (!request.has("scope") && !request.has("type")) request.set("scope", "content");
  if (!request.has("limit")) request.set("limit", "48");
  const [result, collections] = await Promise.all([api(`/library/items?${request}`), api("/collections")]);
  const sort = params.get("sort") || (params.get("q") ? "relevance" : "newest");
  const density = params.get("density") || localStorage.getItem("arivu-library-density") || "comfortable";
  const scope = params.get("scope") === "derived" ? "derived" : "content";
  const typeOptions = scope === "derived" ? ["entity", "concept"] : ["bookmark", "note", "daily_note", "annotation", "knowledge_object"];
  const items = [...(result.items || [])].sort((a, b) => sort === "oldest" ? String(a.updated_at).localeCompare(String(b.updated_at)) : sort === "title" ? String(a.title).localeCompare(String(b.title)) : sort === "domain" ? String(a.source).localeCompare(String(b.source)) : sort === "newest" ? String(b.updated_at).localeCompare(String(a.updated_at)) : 0);
  setRoot(shell("Library", `
    <section class="library-heading">
      <div>
        <p class="lede">Everything you have captured, connected, or developed lives here.</p>
        <p class="meta">${items.length} ${items.length === 1 ? "item" : "items"} in this view${result.next_cursor ? " · more available" : ""}</p>
      </div>
      <div class="button-row">
        <button type="button" id="library-capture">Capture</button>
        <button type="button" class="secondary" id="library-new-object">New object</button>
      </div>
    </section>
    <nav class="library-views" aria-label="Library view">
      <a href="/library"${scope === "content" ? ' aria-current="page"' : ""}>Saved items</a>
      <a href="/library?scope=derived"${scope === "derived" ? ' aria-current="page"' : ""}>Concepts &amp; entities</a>
    </nav>
    ${scope === "derived" ? '<p class="library-view-help">Concepts and entities are generated from your saved material. Explore them here, or use Graph to see how they connect.</p>' : ""}
    ${collectionBrowser(collections, params.get("collection_id") || params.get("collection"))}
    <form class="library-filters" role="search" id="library-filter-form">
      <input type="hidden" name="scope" value="${escapeHTML(scope)}">
      <div class="field library-query"><label for="library-query">Search library</label><input id="library-query" name="q" type="search" value="${escapeHTML(params.get("q") || "")}" placeholder="Title, text, or topic"></div>
      <div class="field"><label for="library-type">Type</label><select id="library-type" name="type">${libraryFilterOptions(typeOptions, params.get("type"), "All types")}</select></div>
      <div class="field"><label for="library-stage">Stage</label><select id="library-stage" name="stage">${libraryFilterOptions(["inbox", "processing", "processed", "archived"], params.get("stage"), "Any stage")}</select></div>
      <div class="field"><label for="library-connection">Connections</label><select id="library-connection" name="connection">${libraryFilterOptions(["connected", "unconnected"], params.get("connection"), "Any state")}</select></div>
      <div class="field"><label for="library-topic">Topic</label><input id="library-topic" name="topic" value="${escapeHTML(params.get("topic") || "")}" placeholder="Topic"></div>
      <div class="field"><label for="library-source">Source</label><input id="library-source" name="source" value="${escapeHTML(params.get("source") || "")}" placeholder="Source"></div>
      <div class="field"><label for="library-date-from">From</label><input id="library-date-from" name="date_from" type="date" value="${escapeHTML(params.get("date_from") || "")}"></div>
      <div class="field"><label for="library-date-to">To</label><input id="library-date-to" name="date_to" type="date" value="${escapeHTML(params.get("date_to") || "")}"></div>
      <div class="field"><label for="library-sort">Sort</label><select id="library-sort" name="sort">${libraryFilterOptions(["relevance", "newest", "oldest", "title", "domain"], sort, "Sort")}</select></div>
      <div class="field"><label for="library-density">Density</label><select id="library-density" name="density">${libraryFilterOptions(["comfortable", "compact"], density, "Density")}</select></div>
      <button type="submit" class="secondary">Apply</button>
    </form>
    <section class="library-list density-${escapeHTML(density)}" aria-label="Library items">
      ${items.map(libraryItem).join("") || emptyState({ eyebrow: "A clear desk", title: "Your library is ready", body: "Capture a link, note, quote, or file. Arivu will keep it even before enrichment or organization.", tag: "section" })}
    </section>
    ${result.next_cursor ? `<p class="pagination"><a class="button secondary" href="/library?${escapeHTML(libraryNextParams(params, result.next_cursor))}">Load more</a></p>` : ""}
  `));
  document.querySelector("#library-capture")?.addEventListener("click", openCaptureComposer);
  document.querySelector("#library-new-object")?.addEventListener("click", openObjectComposer);
  localStorage.setItem("arivu-library-density", density);
  document.querySelector("#library-filter-form")?.addEventListener("submit", (event) => {
    event.preventDefault();
    const next = new URLSearchParams(new FormData(event.currentTarget));
    for (const [key, value] of [...next]) if (!String(value).trim()) next.delete(key);
    navigate(`/library${next.size ? `?${next}` : ""}`);
  });
}

function collectionBrowser(collections, selected) {
  const byParent = new Map();
  for (const item of collections || []) { const key = item.parent_id || ""; byParent.set(key, [...(byParent.get(key) || []), item]); }
  const branch = (parent = "", depth = 0) => depth >= 100 ? "" : (byParent.get(parent) || []).map((item) => `<li><a href="/library?collection_id=${encodeURIComponent(item.id)}"${selected === item.id ? ' aria-current="page"' : ""}>${escapeHTML(item.name)}</a>${byParent.has(item.id) ? `<ul>${branch(item.id, depth + 1)}</ul>` : ""}</li>`).join("");
  const trail = []; let current = (collections || []).find((item) => item.id === selected); for (let depth = 0; current && depth < 100; depth++) { trail.unshift(current); current = (collections || []).find((item) => item.id === current.parent_id); }
  return `<aside class="panel collection-browser" aria-labelledby="collections-heading"><h2 id="collections-heading">Collections</h2>${trail.length ? `<nav aria-label="Collection breadcrumbs"><a href="/library">Library</a> ${trail.map((item) => ` / ${escapeHTML(item.name)}`).join("")}</nav>` : ""}<ul class="collection-tree">${branch() || "<li>No collections yet.</li>"}</ul><p class="meta">Manage collection names and nesting in Settings.</p></aside>`;
}

function libraryFilterOptions(values, selected, emptyLabel) {
  return `<option value="">${escapeHTML(emptyLabel)}</option>${values.map((value) => `<option value="${escapeHTML(value)}"${selected === value ? " selected" : ""}>${escapeHTML(knowledgeTypeLabel(value))}</option>`).join("")}`;
}

function libraryItem(item) {
  const href = knowledgeItemHref(item.type, item.id, item.title);
  const thumbnail = item.type === "bookmark" && item.thumbnail ? `<div class="library-thumbnail" aria-hidden="true"><img src="${escapeHTML(item.thumbnail)}" alt="" loading="lazy" decoding="async"></div>` : "";
  return `<article class="library-row${thumbnail ? " has-thumbnail" : ""}">
    <div class="library-kind" data-kind="${escapeHTML(item.type || "item")}">${escapeHTML(knowledgeTypeLabel(item.type))}</div>
    ${thumbnail}
    <div class="library-copy">
      <h2><a href="${href}">${escapeHTML(item.title || "Untitled")}</a></h2>
      <p>${escapeHTML(String(item.body || "").slice(0, 220))}</p>
      <p class="meta">${[item.source, item.capture_status && `Capture: ${item.capture_status.replaceAll("_", " ")}`, stageLabel(item.stage || ""), item.connection, formatDate(item.updated_at)].filter(Boolean).map(escapeHTML).join(" · ")}</p>
    </div>
    <a class="row-open" href="${href}" aria-label="Open ${escapeHTML(item.title || knowledgeTypeLabel(item.type))}">Open</a>
  </article>`;
}

function preservationPanel(artifacts = [], captureStatus = "saved") {
  const screenshot = artifacts.find((artifact) => artifact.type === "screenshot");
  const preservedPage = artifacts.find((artifact) => artifact.type === "self_contained_html");
  const status = String(captureStatus || "saved").replaceAll("_", " ");
  const messages = {
    saved: "Reader text is stored. This capture did not preserve reader images or additional offline formats; Reprocess can try again.",
    processing: "Browser preservation is still running in the background. The reader remains available as soon as its text is ready.",
    partially_preserved: "The reader is available. One or more preserved formats could not be completed; Reprocess can try them again.",
    preserved: artifacts.length ? "Reader content and preserved files are stored on this instance." : "The reader copy is stored. Additional preserved formats are not enabled for this instance.",
    failed: "The latest preservation attempt failed. Your last good reader copy remains available.",
  };
  const actions = artifacts.map((artifact) => {
    const labels = {
      screenshot: "Open full screenshot",
      pdf: "Open preserved PDF",
      self_contained_html: "Download preserved page",
      source_response: "Download source response",
    };
    const opens = artifact.type === "pdf" || artifact.type === "screenshot";
    return `<li><a href="${escapeHTML(artifact.download_url)}" ${opens ? `target="_blank" rel="noopener"` : "download"}>${escapeHTML(labels[artifact.type] || `Download ${knowledgeTypeLabel(artifact.type)}`)}</a><span class="meta">${escapeHTML(artifact.mime_type)} · ${Math.ceil(Number(artifact.byte_size || 0) / 1024)} KB</span></li>`;
  }).join("");
  const viewer = preservedPage?.preview_url ? `<details class="preserved-page-viewer" data-preserved-page>
    <summary>Preview preserved page</summary>
    <div class="preserved-page-body">
      <p class="meta">This offline copy opens in a locked-down viewer. Scripts, forms, and network access stay disabled.</p>
      <iframe title="Preserved offline page" data-src="${escapeHTML(preservedPage.preview_url)}" sandbox referrerpolicy="no-referrer"></iframe>
    </div>
  </details>` : preservedPage ? `<p class="meta">This preserved page is too large to preview safely. Download it below to open the offline copy.</p>` : "";
  const preview = screenshot ? `<figure class="preservation-preview"><a href="${escapeHTML(screenshot.download_url)}" target="_blank" rel="noopener"><img src="${escapeHTML(screenshot.download_url)}" alt="Preserved full-page screenshot" loading="lazy" decoding="async"></a><figcaption>Visual snapshot captured with the page.</figcaption></figure>` : "";
  return `<section class="preservation" aria-labelledby="preservation-heading">
    <div class="preservation-heading"><div><h2 id="preservation-heading">Preserved copies</h2><p class="preservation-status" data-status="${escapeHTML(captureStatus)}">${escapeHTML(messages[captureStatus] || `Capture status: ${status}.`)}</p></div>${artifacts.length ? `<span class="meta">${artifacts.length} ${artifacts.length === 1 ? "file" : "files"}</span>` : ""}</div>
    ${viewer}${preview}
    ${actions ? `<ul class="preservation-actions">${actions}</ul>` : ""}
  </section>`;
}

function bindPreservedPageViewers() {
  document.querySelectorAll("[data-preserved-page]").forEach((details) => details.addEventListener("toggle", () => {
    if (!details.open) return;
    const frame = details.querySelector("iframe[data-src]");
    if (frame && !frame.src) frame.src = frame.dataset.src;
  }, { once: true }));
}

function knowledgeTypeLabel(value = "item") {
  return String(value).replaceAll("_", " ").replace(/\b\w/g, (letter) => letter.toUpperCase());
}

function evidencePanel(evidence = []) {
  return `<details class="evidence-inspector"><summary>Source evidence (${evidence.length})</summary>${evidence.length ? `<ol>${evidence.map((item) => `<li><div class="evidence-heading"><strong>${escapeHTML(knowledgeTypeLabel(item.kind))}</strong>${item.selected ? `<span class="status success">Selected</span>` : ""}</div><p class="meta">${[item.origin, item.extraction_method, item.quality_status, `Authority ${Number(item.authority || 0)}`, item.extractor_version].filter(Boolean).map((value) => escapeHTML(knowledgeTypeLabel(value))).join(" · ")}</p>${(item.quality_reasons || []).length ? `<p class="meta">Quality notes: ${(item.quality_reasons || []).map((reason) => escapeHTML(knowledgeTypeLabel(reason))).join(", ")}</p>` : ""}${item.canonical_url ? `<p><a href="${escapeHTML(item.canonical_url)}" target="_blank" rel="noreferrer noopener">Open evidence source</a></p>` : ""}${item.preview ? `<details><summary>Preview preserved text</summary><p>${escapeHTML(item.preview)}</p></details>` : ""}</li>`).join("")}</ol>` : `<p class="meta">No preserved source evidence is available yet.</p>`}</details>`;
}

function knowledgeItemHref(type, id, title = "") {
  if (type === "bookmark") return `/bookmark/${encodeURIComponent(id)}`;
  if (type === "note") return `/notes/${encodeURIComponent(id)}`;
  if (type === "daily_note") return `/today?date=${encodeURIComponent(id)}`;
  if (type === "entity" || type === "concept") return `/graph?focus=${encodeURIComponent(`${type}:${id}`)}`;
  return `/library?type=${encodeURIComponent(type || "")}&q=${encodeURIComponent(title || id)}`;
}

function libraryNextParams(params, cursor) {
  const next = new URLSearchParams(params);
  next.set("cursor", cursor);
  next.delete("view");
  return next.toString();
}

async function searchPage() {
  await requireUser();
  const params = new URLSearchParams(location.search);
  if (params.get("review") === "actions") return assistantPage();
  const query = params.get("q") || params.get("search") || "";
  const ask = params.get("mode") === "ask" || params.get("answer") === "1";
  let result = { results: [] };
  if (query) result = await api(`${ask ? "/search/answer" : "/search/items"}?q=${encodeURIComponent(query)}`);
  const results = result.results || result.items || result.citations || [];
  setRoot(shell("Search / Ask", `
    <form class="search-workspace" role="search" id="knowledge-search-form">
      <label for="knowledge-search">Search your knowledge</label>
      <div class="search-line">
        <input id="knowledge-search" type="search" value="${escapeHTML(query)}" placeholder="Find a source, trace a connection, or ask a question" autofocus>
        <button type="submit" name="mode" value="search" class="secondary">Search</button>
        <button type="submit" name="mode" value="ask">Ask</button>
      </div>
      <p class="meta">Ask answers only from material saved in this Arivu instance and keeps links back to the evidence.</p>
    </form>
    ${ask && result.answer ? `<section class="answer-surface"><h2>Answer</h2><p>${escapeHTML(result.answer)}</p></section>` : ""}
    <section class="search-results" aria-live="polite" aria-label="Search results">
      ${results.map(searchResultItem).join("") || (query ? emptyState({ eyebrow: "No match", title: "Try a broader phrase", body: "Search checks titles, saved text, notes, tags, and explicit link context.", tag: "section" }) : emptyState({ eyebrow: "Ready", title: "Start with what you remember", body: "A phrase, source, person, or question is enough.", tag: "section" }))}
    </section>
  `));
  document.querySelector("#knowledge-search-form")?.addEventListener("submit", (event) => {
    event.preventDefault();
    const value = document.querySelector("#knowledge-search").value.trim();
    const mode = event.submitter?.value || "search";
    navigate(`/search?q=${encodeURIComponent(value)}${mode === "ask" ? "&mode=ask" : ""}`);
  });
}

function searchResultItem(item) {
  const type = item.type || item.item_type || "bookmark";
  const id = item.id || item.item_id;
  const href = item.href || knowledgeItemHref(type, id, item.title);
  return `<article class="search-result">
    <p class="meta">${escapeHTML(knowledgeTypeLabel(type))}${item.source ? ` · ${escapeHTML(item.source)}` : ""}</p>
    <h2><a href="${href}">${escapeHTML(item.title || "Untitled")}</a></h2>
    <p>${escapeHTML(item.snippet || item.body || "")}</p>
    ${(item.why_shown || []).length ? `<p class="meta">Why this appeared: ${item.why_shown.map(escapeHTML).join(", ")}</p>` : ""}
  </article>`;
}

async function openCaptureComposer() {
  const body = document.createElement("div");
  body.innerHTML = `
    <form class="form capture-composer" id="capture-composer-form">
      <div class="field">
        <label for="capture-kind">What are you capturing?</label>
        <select id="capture-kind">
          <option value="url">Link</option>
          <option value="note">Note</option>
          <option value="quote">Quote</option>
          <option value="file">File</option>
        </select>
      </div>
      <div data-capture-fields="url quote">
        <div class="field"><label for="capture-url">Source URL</label><input id="capture-url" type="url" placeholder="https://example.com/article"></div>
      </div>
      <div data-capture-fields="note file">
        <div class="field"><label for="capture-title">Title</label><input id="capture-title" type="text" placeholder="A useful working title"></div>
      </div>
      <div data-capture-fields="quote" hidden>
        <div class="field"><label for="capture-quote">Quote</label><textarea id="capture-quote" rows="4" placeholder="Paste the passage you want to remember"></textarea></div>
      </div>
      <div data-capture-fields="note" hidden>
        <div class="field"><label for="capture-note-body">Note</label><textarea id="capture-note-body" rows="6" placeholder="Start writing. You can connect it later."></textarea></div>
      </div>
      <div data-capture-fields="url quote">
        <div class="field"><label for="capture-context">Context</label><textarea id="capture-context" rows="3" placeholder="Why this matters, optional"></textarea></div>
      </div>
      <div data-capture-fields="file" hidden>
        <div class="field"><label for="capture-file">File</label><input id="capture-file" name="file" type="file" accept=".epub,.pdf,.txt,.md,.html,.htm,image/*"></div>
      </div>
      <p class="form-message" data-form-message hidden></p>
      <button type="submit">Save to Arivu</button>
    </form>`;
  const form = body.querySelector("form");
  const kind = body.querySelector("#capture-kind");
  const shared = sharedCaptureParams();
  body.querySelector("#capture-url").value = shared.url || "";
  body.querySelector("#capture-context").value = shared.note || "";
  const sync = () => body.querySelectorAll("[data-capture-fields]").forEach((group) => {
    group.hidden = !group.dataset.captureFields.split(" ").includes(kind.value);
  });
  kind.addEventListener("change", sync);
  sync();
  form.addEventListener("submit", async (event) => {
    event.preventDefault();
    const done = setButtonBusy(event.submitter, "Saving");
    setFormMessage(form);
    try {
      let href = "/library";
      if (kind.value === "note") {
        const result = await api("/notes", { method: "POST", body: JSON.stringify({ title: body.querySelector("#capture-title").value, body: body.querySelector("#capture-note-body").value }) });
        href = `/notes/${result.note.id}`;
      } else if (kind.value === "file") {
        const data = new FormData();
        data.set("title", body.querySelector("#capture-title").value);
        const file = body.querySelector("#capture-file").files[0];
        if (!file) throw new Error("Choose a file to import.");
        data.set("file", file);
        const result = await api("/media/import", { method: "POST", body: data });
        href = result.note?.id ? `/notes/${result.note.id}` : "/library?type=note";
      } else {
        const url = body.querySelector("#capture-url").value.trim();
        if (!url) throw new Error("Source URL is required.");
        const payload = {
          url,
          note: body.querySelector("#capture-context").value,
          quote: kind.value === "quote" ? body.querySelector("#capture-quote").value : "",
        };
        let result;
        try {
          result = await api("/bookmarks", { method: "POST", body: JSON.stringify(payload) });
        } catch (err) {
          if (!navigator.onLine || err.message.includes("couldn't reach Arivu")) {
            queueOfflineBookmark(payload);
            document.querySelector("[data-dialog-close]")?.click();
            ui.toast("Capture queued offline", "success");
            navigate("/library", true);
            return;
          }
          throw err;
        }
        href = `/bookmark/${result.bookmark.id}`;
      }
      document.querySelector("[data-dialog-close]")?.click();
      ui.toast("Captured", "success");
      navigate(href, true);
    } catch (err) {
      setFormMessage(form, err.message);
    } finally {
      done();
    }
  });
  await ui.dialog({ title: "Capture", body, actions: [{ label: "Cancel", value: false, kind: "secondary" }] });
}

async function openCommandPalette() {
  const current = currentItemRef();
  const body = document.createElement("div");
  body.className = "command-palette";
  body.innerHTML = `
    <section>
      <h3>Open</h3>
      <div class="chips">
        ${[
          ["/today", "Today"],
          ["/dashboard", "Capture"],
          ["/inbox", "Inbox"],
          ["/focus", "Focus"],
          ["/review", "Review"],
          ["/notes", "Notes"],
          ["/assistant", "Assistant"],
        ].map(([href, label]) => `<button type="button" class="secondary" data-command-nav="${href}">${label}</button>`).join("")}
      </div>
    </section>
    <form class="form" data-command-save>
      <h3>Save URL</h3>
      <div class="field"><label for="command-url">URL</label><input id="command-url" type="url" placeholder="https://example.com/article"></div>
      <div class="field"><label for="command-save-note">Quick note</label><textarea id="command-save-note" rows="2"></textarea></div>
      <button type="submit">Save</button>
    </form>
    <form class="form" data-command-note>
      <h3>Create note</h3>
      <div class="field"><label for="command-note-title">Title</label><input id="command-note-title" type="text"></div>
      <div class="field"><label for="command-note-body">Body</label><textarea id="command-note-body" rows="3"></textarea></div>
      <button type="submit">Create note</button>
    </form>
    <form class="form" data-command-search>
      <h3>Search</h3>
      <div class="field"><label for="command-query">Query</label><input id="command-query" type="search"></div>
      <div class="button-row">
        <button type="submit" data-command-search-type="search">Search</button>
        <button type="submit" class="secondary" data-command-search-type="answer">Cited answer</button>
      </div>
    </form>
    <form class="form" data-command-current ${current ? "" : "hidden"}>
      <h3>Current item</h3>
      <p class="meta">${current ? `${escapeHTML(current.type)}:${escapeHTML(current.id)}` : "Open a bookmark or note first."}</p>
      <div class="field"><label for="command-task">Task</label><input id="command-task" type="text" placeholder="Next concrete action"></div>
      <div class="field"><label for="command-reminder">Reminder</label><input id="command-reminder" type="datetime-local"></div>
      <div class="field"><label for="command-link-query">Link target search</label><input id="command-link-query" type="search" placeholder="Search bookmark or note title"></div>
      <div class="field"><label for="command-link-target">Link target</label><select id="command-link-target"><option value="">Search to choose target</option></select></div>
      <div class="field"><label for="command-link-label">Link label</label><input id="command-link-label" type="text" placeholder="related"></div>
      <div class="button-row">
        <button type="submit" data-command-current-type="task">Add task</button>
        <button type="submit" class="secondary" data-command-current-type="reminder">Add reminder</button>
        <button type="submit" class="secondary" data-command-current-type="link">Link item</button>
      </div>
    </form>
  `;
  body.querySelectorAll("[data-command-nav]").forEach((button) => {
    button.addEventListener("click", () => {
      document.querySelector("[data-dialog-close]")?.click();
      navigate(button.dataset.commandNav);
    });
  });
  body.querySelector("[data-command-save]").addEventListener("submit", async (event) => {
    event.preventDefault();
    const url = body.querySelector("#command-url").value.trim();
    if (!url) return;
    await commandRun(event.submitter, "Saving", async () => {
      const result = await api("/bookmarks", { method: "POST", body: JSON.stringify({ url, note: body.querySelector("#command-save-note").value }) });
      document.querySelector("[data-dialog-close]")?.click();
      navigate(`/bookmark/${result.bookmark.id}`, true);
      return "Bookmark saved";
    });
  });
  body.querySelector("[data-command-note]").addEventListener("submit", async (event) => {
    event.preventDefault();
    await commandRun(event.submitter, "Creating", async () => {
      const result = await api("/notes", { method: "POST", body: JSON.stringify({ title: body.querySelector("#command-note-title").value, body: body.querySelector("#command-note-body").value }) });
      document.querySelector("[data-dialog-close]")?.click();
      navigate(`/notes/${result.note.id}`, true);
      return "Note created";
    });
  });
  body.querySelector("[data-command-search]").addEventListener("submit", (event) => {
    event.preventDefault();
    const query = body.querySelector("#command-query").value.trim();
    if (!query) return;
    document.querySelector("[data-dialog-close]")?.click();
    if (event.submitter.dataset.commandSearchType === "answer") navigate(`/dashboard?search=${encodeURIComponent(query)}&answer=1`);
    else navigate(`/dashboard?search=${encodeURIComponent(query)}`);
  });
  const linkQuery = body.querySelector("#command-link-query");
  linkQuery?.addEventListener("change", async () => {
    const query = linkQuery.value.trim();
    const target = body.querySelector("#command-link-target");
    target.innerHTML = `<option value="">Searching...</option>`;
    const result = await api(`/link-targets?q=${encodeURIComponent(query)}&limit=20`).catch(() => ({ targets: [] }));
    target.innerHTML = `<option value="">Choose target</option>${(result.targets || []).map((item) => `<option value="${escapeHTML(`${item.type}:${item.id}`)}">${escapeHTML(item.type)} · ${escapeHTML(item.title || item.url || item.id)}</option>`).join("")}`;
  });
  body.querySelector("[data-command-current]")?.addEventListener("submit", async (event) => {
    event.preventDefault();
    if (!current) return;
    const type = event.submitter.dataset.commandCurrentType;
    await commandRun(event.submitter, "Saving", async () => {
      if (type === "task") {
        const title = body.querySelector("#command-task").value.trim();
        if (!title) throw new Error("Task is required.");
        await api("/action-items", { method: "POST", body: JSON.stringify({ item_type: current.type, item_id: current.id, title }) });
        return "Task added";
      }
      if (type === "reminder") {
        const due = body.querySelector("#command-reminder").value;
        if (!due) throw new Error("Reminder time is required.");
        await api("/reminders", { method: "POST", body: JSON.stringify({ item_type: current.type, item_id: current.id, due_at: new Date(due).toISOString(), notification_channel: "in_app" }) });
        return "Reminder added";
      }
      const rawTarget = body.querySelector("#command-link-target").value;
      const [toType, toID] = rawTarget.split(":");
      if (!toType || !toID) throw new Error("Link target is required.");
      await api("/links", { method: "POST", body: JSON.stringify({ from_type: current.type, from_id: current.id, to_type: toType, to_id: toID, label: body.querySelector("#command-link-label").value || "related" }) });
      return "Link created";
    });
  });
  await ui.dialog({ title: "Quick actions", body, actions: [{ label: "Close", value: true, kind: "secondary" }] });
}

async function commandRun(button, busyLabel, action) {
  const done = setButtonBusy(button, busyLabel);
  try {
    const message = await action();
    ui.toast(message, "success");
  } catch (err) {
    ui.toast(err.message, "error");
  } finally {
    done();
  }
}

async function dashboardPage() {
  await requireUser();
  const [bookmarks, savedSearches] = await Promise.all([
    api(`/bookmarks${location.search}`),
    api("/saved-searches").catch(() => ({ saved_searches: [] })),
  ]);
  const bookmarkList = bookmarks || [];
  const params = new URLSearchParams(location.search);
  const shared = sharedCaptureParams();
  setRoot(shell("Capture", `
    <section class="split">
      <form class="panel form" id="save-form">
        <span class="meta">Capture</span>
        <h2>Save a page into Inbox</h2>
        <div class="field"><label for="url">URL</label><input id="url" type="url" placeholder="https://example.com/article" value="${escapeHTML(shared.url)}" required></div>
        <div class="field"><label for="save-note">Quick note</label><textarea id="save-note" rows="2" placeholder="Why this matters, optional">${escapeHTML(shared.note)}</textarea>${voiceButton("save-note", "quick note")}</div>
        <div class="field"><label for="save-tags">Tags</label><input id="save-tags" type="text" placeholder="research, idea, later"></div>
        <button type="submit">Save bookmark</button>
        ${offlineQueueMessage()}
        <p class="meta" id="job-status" hidden></p>
      </form>
      <section class="panel">
        <span class="meta">Work loop</span>
        <h2>Capture, decide, act, review</h2>
        <p>New saves land in Inbox. Move active work to Working, keep finished references in Kept, then let Review bring back what matters.</p>
        <div class="chips">
          <a href="/inbox">Inbox</a>
          <a href="/focus">Focus</a>
          <a href="/review">Review</a>
        </div>
      </section>
    </section>
    <form class="toolbar" role="search" id="search-form">
      <label class="sr-only" for="search">Search bookmarks</label>
      <input id="search" type="search" placeholder="Search saved pages" value="${escapeHTML(params.get("search") || "")}">
      <button id="search-button" class="secondary" type="submit">Search</button>
      <button id="answer-button" class="secondary" type="button">Ask saved pages</button>
      <details>
        <summary>Filters</summary>
        <label class="sr-only" for="filter-tag">Filter by tag</label>
        <input id="filter-tag" type="text" placeholder="Tag" value="${escapeHTML(params.get("tag") || "")}">
        <label class="sr-only" for="filter-domain">Filter by domain</label>
        <input id="filter-domain" type="text" placeholder="Domain" value="${escapeHTML(params.get("domain") || "")}">
        <label class="sr-only" for="filter-source">Filter by source</label>
        <input id="filter-source" type="text" placeholder="Source" value="${escapeHTML(params.get("source") || "")}">
        <input id="filter-date-from" type="date" aria-label="Saved after" value="${escapeHTML(params.get("date_from") || "")}">
        <input id="filter-date-to" type="date" aria-label="Saved before" value="${escapeHTML(params.get("date_to") || "")}">
        <label class="sr-only" for="filter-read">Filter by read status</label>
        <select id="filter-read">
          <option value="">Any status</option>
          <option value="unread" ${params.get("read_status") === "unread" ? "selected" : ""}>Unread</option>
          <option value="read" ${params.get("read_status") === "read" ? "selected" : ""}>Read</option>
        </select>
      </details>
    </form>
    <details class="panel disclosure-panel">
      <summary><strong>Saved searches</strong> · ${Number((savedSearches.saved_searches || []).length)} saved</summary>
      <section class="split disclosure-body embedded-split">
        <form class="form" id="saved-search-form">
          <h2>Save this search</h2>
          <div class="field"><label for="saved-search-name">Name</label><input id="saved-search-name" type="text" placeholder="Unread research"></div>
          <p class="form-message" id="saved-search-message" data-form-message hidden></p>
          <button type="submit">Save current search</button>
        </form>
        <section>
          <h2>Saved searches</h2>
          ${savedSearchList(savedSearches.saved_searches || [])}
        </section>
      </section>
    </details>
    <section class="panel" id="answer-panel" hidden></section>
    <section class="grid" aria-label="Bookmarks">
      ${bookmarkList.map(bookmarkCard).join("") || workflowEmptyState()}
    </section>
  `));
  const saveForm = document.querySelector("#save-form");
  bindVoiceCapture();
  saveForm.insertAdjacentHTML("beforeend", `<p class="form-message" id="save-message" data-form-message hidden></p>`);
  saveForm.addEventListener("submit", async (event) => {
    event.preventDefault();
    const done = setButtonBusy(event.submitter, "Saving bookmark");
    const payload = {
      url: document.querySelector("#url").value,
      note: document.querySelector("#save-note").value,
      tags: splitTags(document.querySelector("#save-tags").value),
    };
    setFormMessage(saveForm);
    try {
      const result = await api("/bookmarks", { method: "POST", body: JSON.stringify(payload) });
      ui.toast("Bookmark saved", "success");
      await showJobStatus(result.job_id);
      navigate(`/bookmark/${result.bookmark.id}`, true);
    } catch (err) {
      if (!navigator.onLine || err.message.includes("couldn't reach Arivu")) {
        queueOfflineBookmark(payload);
        setFormMessage(saveForm, "Saved offline. Arivu will sync it when this browser is online.", "success");
        ui.toast("Bookmark queued offline", "success");
        saveForm.reset();
        return;
      }
      setFormMessage(saveForm, err.message);
      ui.toast(err.message, "error");
    } finally {
      done();
    }
  });
  document.querySelector("#search-form").addEventListener("submit", (event) => {
    event.preventDefault();
    navigate(`/dashboard${dashboardQueryString()}`);
  });
  document.querySelector("#answer-button").addEventListener("click", async (event) => {
    const query = document.querySelector("#search").value.trim();
    if (!query) {
      ui.toast("Enter a search query first", "error");
      return;
    }
    const done = setButtonBusy(event.currentTarget, "Answering");
    const panel = document.querySelector("#answer-panel");
    try {
      const answer = await api(`/search/answer${dashboardQueryString("q")}`);
      panel.hidden = false;
      panel.innerHTML = answerPanel(answer);
      bindFeedbackControls();
    } catch (err) {
      ui.toast(err.message, "error");
    } finally {
      done();
    }
  });
  if (params.get("answer") === "1" && document.querySelector("#search").value.trim()) {
    document.querySelector("#answer-button").click();
  }
  const savedSearchForm = document.querySelector("#saved-search-form");
  savedSearchForm.addEventListener("submit", async (event) => {
    event.preventDefault();
    const done = setButtonBusy(event.submitter, "Saving search");
    setFormMessage(savedSearchForm);
    try {
      const query = document.querySelector("#search").value.trim();
      await api("/saved-searches", {
        method: "POST",
        body: JSON.stringify({
          name: document.querySelector("#saved-search-name").value,
          query,
          filters: dashboardFilters(),
        }),
      });
      ui.toast("Search saved", "success");
      render();
    } catch (err) {
      setFormMessage(savedSearchForm, err.message);
      ui.toast(err.message, "error");
    } finally {
      done();
    }
  });
}

function workflowEmptyState() {
  return `<div class="panel empty-state">
    <span class="meta">First save</span>
    <h2>No bookmarks yet</h2>
    <p>Save a URL above. Arivu will archive it, extract text, and queue it in Inbox for triage.</p>
    <div class="chips">
      <a href="/dashboard">Capture</a>
      <a href="/inbox">Inbox</a>
      <a href="/focus">Focus</a>
      <a href="/review">Review</a>
    </div>
  </div>`;
}

function emptyState({ eyebrow, title, body, tag = "div", panel = true, headingLevel = 2 }) {
  const safeTag = tag === "article" || tag === "section" ? tag : "div";
  const safeHeadingLevel = headingLevel === 3 ? 3 : 2;
  return `<${safeTag} class="${panel ? "panel " : ""}empty-state">
    <span class="meta">${escapeHTML(eyebrow)}</span>
    <h${safeHeadingLevel}>${escapeHTML(title)}</h${safeHeadingLevel}>
    <p>${escapeHTML(body)}</p>
  </${safeTag}>`;
}

function dashboardFilters() {
  return {
    tag: document.querySelector("#filter-tag")?.value.trim() || "",
    domain: document.querySelector("#filter-domain")?.value.trim() || "",
    source: document.querySelector("#filter-source")?.value.trim() || "",
    date_from: document.querySelector("#filter-date-from")?.value || "",
    date_to: document.querySelector("#filter-date-to")?.value || "",
    read_status: document.querySelector("#filter-read")?.value || "",
  };
}

function dashboardQueryString(searchKey = "search") {
  const params = new URLSearchParams();
  const query = document.querySelector("#search").value.trim();
  if (query) params.set(searchKey, query);
  const filters = dashboardFilters();
  for (const [key, value] of Object.entries(filters)) {
    if (value) params.set(key, value);
  }
  const raw = params.toString();
  return raw ? `?${raw}` : "";
}

function savedSearchList(items) {
  if (!items.length) return `<p class="meta">No saved searches yet.</p>`;
  return `<div class="stack">${items.map((item) => {
    const filters = item.filters || {};
    const params = new URLSearchParams();
    if (item.query) params.set("search", item.query);
    for (const key of ["tag", "domain", "source", "date_from", "date_to", "read_status"]) {
      if (filters[key]) params.set(key, filters[key]);
    }
    return `<a class="text-link" href="/dashboard?${params.toString()}">${escapeHTML(item.name)}</a>`;
  }).join("")}</div>`;
}

function answerPanel(answer) {
  const citations = answer.citations || [];
  return `<h2>Cited answer</h2>
    <p>${escapeHTML(answer.answer || "")}</p>
    <div class="stack">${citations.map((item, index) => {
      const itemID = encodeURIComponent(item.id || "");
      return `<article class="annotation">
      <p><strong>[${index + 1}] ${escapeHTML(item.title || item.url)}</strong> <span class="meta">${escapeHTML(item.type || "bookmark")} · ${escapeHTML(item.domain || "")}</span></p>
      <p>${escapeHTML(item.snippet || "")}</p>
      ${feedbackControls(item.type || "bookmark", item.id || "", "answer", item.feedback_state)}
      ${item.why_shown?.length ? `<p class="meta">Why shown: ${item.why_shown.map(escapeHTML).join(" · ")} · freshness ${Number(item.freshness_score || 0)}</p>` : ""}
      <a class="text-link" href="${item.type === "note" ? `/notes/${itemID}` : `/bookmark/${itemID}`}">Open citation</a>
    </article>`;
    }).join("") || `<p class="meta">No citations found.</p>`}</div>`;
}

function sharedCaptureParams() {
  const params = new URLSearchParams(location.search);
  const text = params.get("text") || "";
  const title = params.get("title") || "";
  const directURL = params.get("url") || "";
  const match = text.match(/https?:\/\/\S+/i);
  const url = directURL || (match ? match[0].replace(/[),.;]+$/, "") : "");
  const note = [title, text.replace(url, "").trim()].filter(Boolean).join("\n\n");
  return { url, note };
}

function bookmarkCard(b) {
  const bookmarkID = encodeURIComponent(b.id || "");
  return `<a class="panel bookmark" href="/bookmark/${bookmarkID}">
    <span class="meta">${escapeHTML(b.domain || "web")} · ${Number(b.reading_time || 0)} min</span>
    <h2>${escapeHTML(b.title || b.url)}</h2>
    <p>${escapeHTML(b.description || "No description available.")}</p>
  </a>`;
}

function splitTags(value) {
  return String(value || "").split(",").map((tag) => tag.trim()).filter(Boolean).slice(0, 20);
}

function stageLabel(stage) {
  return {
    inbox: "Inbox",
    processing: "Working",
    processed: "Kept",
    archived: "Archived",
  }[stage] || stage;
}

function focusViewLabel(view) {
  return {
    pending: "Pending",
    overdue: "Overdue",
    today: "Today",
    upcoming: "Upcoming",
    completed: "Completed",
  }[view] || view;
}

async function inboxPage() {
  await requireUser();
  const params = new URLSearchParams(location.search);
  const stage = params.get("stage") || "inbox";
  const result = await api(`/inbox?stage=${encodeURIComponent(stage)}&limit=100`);
  const items = result.items || [];
  const counts = result.counts || {};
  setRoot(shell("Inbox", `
    <section class="split">
      <section class="panel">
        <span class="meta">Triage loop</span>
        <h2>${Number(counts.inbox || 0)} unprocessed</h2>
        <p>Decide why each saved item matters, move active work to Working, then mark finished references Kept before review.</p>
      </section>
      <section class="panel">
        <h2>Stages</h2>
        <div class="chips stage-tabs">
          ${["inbox", "processing", "processed", "archived"].map((name) => `<a class="${name === stage ? "active" : ""}" ${name === stage ? `aria-current="page"` : ""} href="/inbox?stage=${name}">${escapeHTML(stageLabel(name))} · ${Number(counts[name] || 0)}</a>`).join("")}
        </div>
      </section>
    </section>
    <section class="panel bulk-toolbar" data-inbox-bulk>
      <span class="meta"><span data-bulk-count>0</span> selected</span>
      <div class="button-row">
        <button type="button" class="secondary" data-bulk-stage="processing">Working</button>
        <button type="button" class="secondary" data-bulk-stage="processed">Kept</button>
        <button type="button" class="secondary" data-bulk-stage="archived">Archive</button>
      </div>
    </section>
    <section class="stack">
      ${items.map(inboxCard).join("") || emptyState({ eyebrow: "Clear", title: `No ${stageLabel(stage)} items`, body: "New captures and notes appear in Inbox until you decide what to do with them." })}
    </section>
  `));
  document.querySelectorAll("[data-inbox-select]").forEach((checkbox) => {
    checkbox.addEventListener("change", updateBulkSelectionCount);
  });
  document.querySelectorAll("[data-bulk-stage]").forEach((button) => {
    button.addEventListener("click", () => bulkUpdateInbox(button.dataset.bulkStage, button));
  });
  document.querySelectorAll("[data-inbox-stage]").forEach((button) => {
    button.addEventListener("click", () => updateInboxItem(button, button.dataset.inboxStage));
  });
  document.querySelectorAll("[data-inbox-save]").forEach((button) => {
    button.addEventListener("click", () => updateInboxItem(button, button.closest("[data-inbox-item]").querySelector("[data-next-stage]").value));
  });
  bindActionItemControls();
  bindPriorityButtons();
  ui.on(document, "keydown", inboxKeyboardTriage);
  updateBulkSelectionCount();
}

function inboxCard(item) {
  const itemID = `${item.item_type}:${item.id}`;
  const isNote = item.item_type === "note";
  return `<article class="panel form" data-inbox-item="${escapeHTML(itemID)}">
    <label class="meta"><input type="checkbox" data-inbox-select value="${escapeHTML(itemID)}"> ${escapeHTML(item.item_type || "item")} · ${escapeHTML(item.domain || item.source || item.stage || "")}</label>
    <h2>${escapeHTML(item.title || item.url || "Untitled")}</h2>
    <p>${escapeHTML(item.description || item.url || "")}</p>
    <div class="split compact-split">
      <div class="field">
        <label for="next-action-${escapeHTML(item.id)}">Next action</label>
        <input id="next-action-${escapeHTML(item.id)}" data-next-action value="${escapeHTML(item.next_action || "")}" maxlength="500" placeholder="Why keep this? What will you do with it?">
      </div>
      <fieldset class="field priority-field">
        <legend>Priority</legend>
        <input data-importance type="hidden" value="${Number(item.importance || 0)}">
        <div class="priority-buttons">${priorityButtons(item.importance || 0)}</div>
      </fieldset>
      <div class="field">
        <label for="stage-${escapeHTML(item.id)}">Stage</label>
        <select id="stage-${escapeHTML(item.id)}" data-next-stage>
          ${["inbox", "processing", "processed", "archived"].map((stage) => `<option value="${stage}" ${stage === item.stage ? "selected" : ""}>${escapeHTML(stageLabel(stage))}</option>`).join("")}
        </select>
      </div>
    </div>
    <p class="button-row">
      <a class="button secondary" href="${isNote ? `/notes/${encodeURIComponent(item.id)}` : `/bookmark/${escapeHTML(item.id)}`}">Open</a>
      <button type="button" data-inbox-save="${escapeHTML(itemID)}">Save state</button>
      <button type="button" class="secondary" data-inbox-stage="processing">Working</button>
      <button type="button" class="secondary" data-inbox-stage="processed">Kept</button>
      <button type="button" class="secondary" data-inbox-stage="archived">Archive</button>
    </p>
    ${actionItemsPanel(item.item_type, item.id, item.action_items || [])}
  </article>`;
}

function selectedInboxItems() {
  return Array.from(document.querySelectorAll("[data-inbox-select]:checked")).map((item) => item.value);
}

function updateBulkSelectionCount() {
  const target = document.querySelector("[data-bulk-count]");
  if (target) target.textContent = String(selectedInboxItems().length);
}

async function bulkUpdateInbox(stage, button) {
  const items = selectedInboxItems();
  if (!items.length) {
    ui.toast("Select inbox items first.", "error");
    return;
  }
  const done = setButtonBusy(button, "Saving");
  try {
    const result = await api("/inbox/bulk", { method: "POST", body: JSON.stringify({ items, stage }) });
    ui.toast(`${result.updated_count || 0} item${result.updated_count === 1 ? "" : "s"} updated`, result.failed_count ? "error" : "success");
    render();
  } catch (err) {
    ui.toast(err.message, "error");
  } finally {
    done();
  }
}

function inboxKeyboardTriage(event) {
  if (event.metaKey || event.ctrlKey || event.altKey || event.target.matches("input, textarea, select")) return;
  const card = event.target.closest?.("[data-inbox-item]");
  if (!card) return;
  const shortcuts = { p: "processing", d: "processed", a: "archived" };
  const stage = shortcuts[event.key.toLowerCase()];
  if (!stage) return;
  event.preventDefault();
  const button = card.querySelector(`[data-inbox-stage="${stage}"]`) || card.querySelector("[data-inbox-save]");
  updateInboxItem(button, stage);
}

async function updateInboxItem(button, stage) {
  const card = button.closest("[data-inbox-item]");
  const done = setButtonBusy(button, "Saving");
  try {
    await saveItemState(card.dataset.inboxItem, stage, Number(card.querySelector("[data-importance]").value || 0), card.querySelector("[data-next-action]").value);
    ui.toast("Inbox updated", "success");
    render();
  } catch (err) {
    ui.toast(err.message, "error");
  } finally {
    done();
  }
}

function priorityButtons(value) {
  const current = Number(value || 0);
  return [
    [1, "Low"],
    [3, "Med"],
    [5, "High"],
  ].map(([score, label]) => `<button type="button" class="secondary ${current === score ? "active" : ""}" data-priority="${score}" aria-pressed="${current === score}">${label}</button>`).join("");
}

function bindPriorityButtons() {
  document.querySelectorAll("[data-priority]").forEach((button) => {
    button.addEventListener("click", () => {
      const field = button.closest(".priority-field");
      field.querySelector("[data-importance]").value = button.dataset.priority;
      field.querySelectorAll("[data-priority]").forEach((item) => item.classList.toggle("active", item === button));
      field.querySelectorAll("[data-priority]").forEach((item) => item.setAttribute("aria-pressed", String(item === button)));
    });
  });
}

function saveItemState(itemID, stage, importance, nextAction) {
  return api(`/inbox/${itemID}`, {
    method: "PATCH",
    body: JSON.stringify({ stage, importance, next_action: nextAction }),
  });
}

async function focusPage() {
  await requireUser();
  const params = new URLSearchParams(location.search);
  const view = params.get("focus") || (location.pathname === "/focus" ? params.get("view") : "") || "pending";
  const [actions, reminders] = await Promise.all([
    api("/action-items?status=all"),
    api("/reminders?status=all"),
  ]);
  const actionItems = focusActionFilter(actions.action_items || [], view);
  const reminderItems = focusReminderFilter(reminders.reminders || [], view);
  setRoot(shell("Focus", `<div class="home-view focus-view">
    ${homeViewTabs("focus")}
    <section class="focus-overview">
      <section class="panel">
        <span class="meta">${escapeHTML(focusViewLabel(view))}</span>
        <h2>${actionItems.length + reminderItems.length} tasks and reminders</h2>
        <p>Start with concrete tasks, then check timed reminders. Every item links back to its source.</p>
      </section>
      <section class="panel">
        <h2>Queue</h2>
        <div class="chips">
          <a href="/inbox?stage=processing">Working</a>
          <a href="/today?view=review">Review</a>
          <a href="/assistant">Assistant</a>
        </div>
      </section>
    </section>
    <nav class="chips stage-tabs focus-filters" aria-label="Focus filters">
      ${["pending", "overdue", "today", "upcoming", "completed"].map((name) => `<a class="${name === view ? "active" : ""}" ${name === view ? `aria-current="page"` : ""} href="/today?view=focus&amp;focus=${name}">${escapeHTML(focusViewLabel(name))}</a>`).join("")}
    </nav>
    <section class="focus-columns">
      <section class="panel">
        <h2>Action items</h2>
        ${focusActionItems(actionItems, view)}
      </section>
      <section class="panel">
        <h2>Reminders</h2>
        ${focusReminders(reminderItems, view)}
      </section>
    </section>
  </div>`));
  bindActionItemControls();
  bindReminderControls();
}

function focusActionFilter(items, view) {
  if (view === "completed") return items.filter((item) => item.status === "completed");
  if (view === "pending") return items.filter((item) => item.status !== "completed");
  return [];
}

function focusReminderFilter(items, view) {
  if (view === "completed") return items.filter((item) => item.status === "completed");
  if (view === "pending") return items.filter((item) => item.status !== "completed");
  return items.filter((item) => item.status !== "completed" && item.due_state === view);
}

function focusActionItems(items, view) {
  if (!items.length) return focusEmptyState("action", view);
  return `<div class="stack">${items.map((item) => `<article class="annotation">
    <p><strong>${escapeHTML(item.title || "Action item")}</strong> <span class="meta">${escapeHTML(item.item_type || "item")} · ${escapeHTML(item.item_title || "")}</span></p>
    <p class="button-row">
      <a class="button secondary" href="${itemHref(item)}">Open item</a>
      <button type="button" data-action-item-complete="${escapeHTML(item.id)}" aria-label="Complete action item ${escapeHTML(item.title || "")}">Complete</button>
      <button type="button" class="danger" data-action-item-delete="${escapeHTML(item.id)}" aria-label="Delete action item ${escapeHTML(item.title || "")}">Delete task</button>
    </p>
  </article>`).join("")}</div>`;
}

function focusReminders(items, view) {
  if (!items.length) return focusEmptyState("reminder", view);
  return `<div class="stack">${items.map((item) => `<article class="annotation">
    <p><strong>${escapeHTML(formatDate(item.due_at))}</strong> <span class="meta">${reminderMeta(item)}</span></p>
    ${item.note ? `<p>${escapeHTML(item.note)}</p>` : ""}
    <p class="button-row">
      <a class="button secondary" href="${itemHref(item)}">Open item</a>
      <button type="button" class="secondary" data-reminder-snooze="${escapeHTML(item.id)}" data-minutes="30">30m</button>
      <button type="button" class="secondary" data-reminder-snooze="${escapeHTML(item.id)}" data-days="1">Tomorrow</button>
      <button type="button" data-reminder-complete="${escapeHTML(item.id)}" aria-label="Complete reminder ${escapeHTML(item.note || item.item_title || "")}">Complete</button>
      <button type="button" class="danger" data-reminder-delete="${escapeHTML(item.id)}" aria-label="Delete reminder ${escapeHTML(item.note || item.item_title || "")}">Delete reminder</button>
    </p>
  </article>`).join("")}</div>`;
}

function focusEmptyState(type, view) {
  const label = {
    pending: "pending",
    overdue: "overdue",
    today: "due today",
    upcoming: "upcoming",
    completed: "completed",
  }[view] || view;
  if (type === "action" && view !== "pending" && view !== "completed") {
    return emptyState({ eyebrow: "Clear", title: `No ${label} action items`, body: "Action items stay in pending or completed. Timed work appears under reminders.", panel: false, headingLevel: 3 });
  }
  const noun = type === "action" ? "action items" : "reminders";
  const body = type === "action" ? "Tasks added from Inbox or a saved item appear here." : "Timed nudges from saved items appear here.";
  return emptyState({ eyebrow: "Clear", title: `No ${label} ${noun}`, body, panel: false, headingLevel: 3 });
}

function itemHref(item) {
  const id = encodeURIComponent(item.item_id || "");
  return item.item_type === "note" ? `/notes/${id}` : `/bookmark/${id}`;
}

async function assistantPage() {
  await requireUser();
  const params = new URLSearchParams(location.search);
  const status = params.get("status") || "pending";
  const result = await api(`/assistant/actions?status=${encodeURIComponent(status)}`);
  const actions = result.actions || [];
  setRoot(shell("Assistant", `
    <section class="split">
      <form class="panel form" id="assistant-suggest-form">
        <span class="meta">Planner</span>
        <h2>Draft suggested actions</h2>
        <p>Arivu prepares suggestions only. Nothing changes until you review and run a proposal.</p>
        <div class="field">
          <label for="assistant-suggest-mode">Context</label>
          <select id="assistant-suggest-mode" name="mode">
            <option value="inbox">Inbox stage</option>
            <option value="review">Review queue</option>
            <option value="search">Search query</option>
            <option value="item">Specific item</option>
          </select>
        </div>
        <div class="field"><label for="assistant-suggest-stage">Stage</label><select id="assistant-suggest-stage" name="stage">${["inbox", "processing", "processed", "archived"].map((stage) => `<option value="${stage}">${escapeHTML(stageLabel(stage))}</option>`).join("")}</select></div>
        <div class="field"><label for="assistant-suggest-query">Search</label><input id="assistant-suggest-query" name="query" type="search" maxlength="2000" placeholder="recall, launch, research"></div>
        <div class="split compact-split">
          <div class="field"><label for="assistant-suggest-type">Item type</label><select id="assistant-suggest-type" name="item_type"><option value="bookmark">Bookmark</option><option value="note">Note</option></select></div>
          <div class="field"><label for="assistant-suggest-id">Item ID</label><input id="assistant-suggest-id" name="item_id" type="text" autocomplete="off"></div>
        </div>
        <div class="field"><label for="assistant-suggest-limit">Drafts</label><input id="assistant-suggest-limit" name="limit" type="number" min="1" max="12" value="6"></div>
        <p class="form-message" data-form-message hidden></p>
        <button type="submit">Generate drafts</button>
      </form>
      <section class="panel">
        <span class="meta">Pending proposals</span>
        <h2>${actions.length} ${escapeHTML(status)} proposals</h2>
        <p>Drafts do nothing until you queue them. Review the JSON, then execute or reject each proposal.</p>
      </section>
    </section>
    <section class="stack" id="assistant-drafts" aria-live="polite"></section>
    <details class="panel form">
      <summary>Manual proposal JSON</summary>
      <form id="assistant-action-form">
        <div class="field">
          <label for="assistant-action-type">Action</label>
          <select id="assistant-action-type">
            <option value="update_item_state">Update item state</option>
            <option value="create_link">Create link</option>
            <option value="create_reminder">Create reminder</option>
            <option value="create_action_item">Create action item</option>
          </select>
        </div>
        <div class="field">
          <label for="assistant-payload">Payload JSON</label>
          <textarea id="assistant-payload" rows="7" spellcheck="false">{&#10;  "item_type": "bookmark",&#10;  "item_id": "",&#10;  "stage": "processing",&#10;  "importance": 3,&#10;  "next_action": ""&#10;}</textarea>
        </div>
        <p class="form-message" id="assistant-message" data-form-message hidden></p>
        <button type="submit">Add to review</button>
      </form>
    </details>
    <section class="panel">
      <h2>Queue</h2>
      <div class="chips stage-tabs">
        ${["pending", "executed", "failed", "rejected", "all"].map((name) => `<a class="${name === status ? "active" : ""}" ${name === status ? `aria-current="page"` : ""} href="/assistant?status=${name}">${name}</a>`).join("")}
      </div>
    </section>
    <section class="stack">
      ${actions.map(assistantActionCard).join("") || emptyState({ eyebrow: "No proposals", title: "Nothing waiting", body: "Queued assistant proposals appear here for review." })}
    </section>
  `));
  document.querySelector("#assistant-suggest-form").addEventListener("submit", submitAssistantSuggestions);
  const form = document.querySelector("#assistant-action-form");
  form.addEventListener("submit", submitAssistantAction);
  document.querySelector("#assistant-action-type").addEventListener("change", updateAssistantPayloadTemplate);
  document.querySelectorAll("[data-assistant-approve]").forEach((button) => {
    button.addEventListener("click", () => decideAssistantAction(button, "approve"));
  });
  document.querySelectorAll("[data-assistant-reject]").forEach((button) => {
    button.addEventListener("click", () => decideAssistantAction(button, "reject"));
  });
}

function assistantActionCard(action) {
  const payload = JSON.stringify(action.payload || {}, null, 2);
  const result = JSON.stringify(action.result || {}, null, 2);
  const actionID = escapeHTML(action.id || "");
  const isPending = action.status === "pending";
  return `<article class="panel form">
    <span class="meta">${escapeHTML(action.action_type || "action")} · ${escapeHTML(action.status || "")} · ${escapeHTML(formatDate(action.created_at))}</span>
    <h2>${escapeHTML(assistantActionTitle(action))}</h2>
    ${action.error ? `<p class="form-message form-message-error">${escapeHTML(action.error)}</p>` : ""}
    <div class="split comparison-split">
      <div class="field">
        <label>Payload</label>
        <pre class="code-block">${escapeHTML(payload)}</pre>
      </div>
      <div class="field">
        <label>Result</label>
        <pre class="code-block">${escapeHTML(result)}</pre>
      </div>
    </div>
    <p class="button-row">
      ${isPending ? `<button type="button" data-assistant-approve="${actionID}">Execute proposal</button><button type="button" class="secondary" data-assistant-reject="${actionID}">Reject proposal</button>` : ""}
    </p>
  </article>`;
}

function assistantActionTitle(action) {
  const payload = action.payload || {};
  if (action.action_type === "update_item_state") return `${payload.item_type || "item"}:${payload.item_id || ""} -> ${payload.stage || "stage"}`;
  if (action.action_type === "create_link") return `${payload.from_type || "item"}:${payload.from_id || ""} links to ${payload.to_type || "item"}:${payload.to_id || ""}`;
  if (action.action_type === "create_reminder") return `${payload.item_type || "item"}:${payload.item_id || ""} reminder`;
  return action.action_type || "Assistant action";
}

async function submitAssistantSuggestions(event) {
  event.preventDefault();
  const form = event.currentTarget;
  const done = setButtonBusy(event.submitter, "Generating");
  setFormMessage(form);
  try {
    const payload = Object.fromEntries(new FormData(form).entries());
    payload.limit = Number(payload.limit || 6);
    const result = await api("/assistant/suggestions", { method: "POST", body: JSON.stringify(payload) });
    renderAssistantDrafts(result.suggestions || []);
  } catch (err) {
    setFormMessage(form, err.message);
    ui.toast(err.message, "error");
  } finally {
    done();
  }
}

function renderAssistantDrafts(drafts) {
  const target = document.querySelector("#assistant-drafts");
  target.innerHTML = drafts.map(assistantDraftCard).join("") || emptyState({ eyebrow: "No drafts", title: "No reviewable draft found", body: "Try a different source or create a manual proposal." });
  target.querySelectorAll("[data-assistant-draft]").forEach((button) => {
    button.addEventListener("click", () => queueAssistantDraft(button));
  });
}

function assistantDraftCard(draft) {
  const payload = JSON.stringify(draft.payload || {}, null, 2);
  const source = draft.source || {};
  const encoded = escapeHTML(JSON.stringify({ action_type: draft.action_type, payload: draft.payload || {} }));
  return `<article class="panel form">
    <span class="meta">${escapeHTML(draft.action_type || "action")} · ${escapeHTML(source.title || source.item_id || "")}</span>
    <h2>${escapeHTML(draft.title || "Assistant draft")}</h2>
    <p>${escapeHTML(draft.reason || "")}</p>
    <div class="field">
      <label>Payload</label>
      <pre class="code-block">${escapeHTML(payload)}</pre>
    </div>
    <p class="button-row">
      ${source.href ? `<a class="button secondary" href="${escapeHTML(source.href)}">Open source</a>` : ""}
      <button type="button" data-assistant-draft="${encoded}">Queue proposal</button>
    </p>
  </article>`;
}

async function queueAssistantDraft(button) {
  const draft = JSON.parse(button.dataset.assistantDraft || "{}");
  const done = setButtonBusy(button, "Queueing");
  try {
    await api("/assistant/actions", { method: "POST", body: JSON.stringify(draft) });
    ui.toast("Assistant proposal queued", "success");
    navigate("/assistant?status=pending", true);
  } catch (err) {
    ui.toast(err.message, "error");
  } finally {
    done();
  }
}

async function submitAssistantAction(event) {
  event.preventDefault();
  const form = event.currentTarget;
  const done = setButtonBusy(event.submitter, "Adding");
  setFormMessage(form);
  try {
    const payload = JSON.parse(document.querySelector("#assistant-payload").value || "{}");
    await api("/assistant/actions", {
      method: "POST",
      body: JSON.stringify({ action_type: document.querySelector("#assistant-action-type").value, payload }),
    });
    ui.toast("Assistant action queued", "success");
    navigate("/assistant?status=pending", true);
  } catch (err) {
    setFormMessage(form, err.message);
    ui.toast(err.message, "error");
  } finally {
    done();
  }
}

function updateAssistantPayloadTemplate(event) {
  const templates = {
    update_item_state: {
      item_type: "bookmark",
      item_id: "",
      stage: "processing",
      importance: 3,
      next_action: "",
    },
    create_link: {
      from_type: "bookmark",
      from_id: "",
      to_type: "note",
      to_id: "",
      label: "supports",
    },
    create_reminder: {
      item_type: "bookmark",
      item_id: "",
      due_at: new Date(Date.now() + 86400000).toISOString(),
      timezone: browserTimezone(),
      recurrence: "none",
      recurrence_interval_days: 0,
      notification_channel: "in_app",
      note: "",
    },
    create_action_item: {
      item_type: "bookmark",
      item_id: "",
      title: "",
    },
  };
  document.querySelector("#assistant-payload").value = JSON.stringify(templates[event.currentTarget.value], null, 2);
}

async function decideAssistantAction(button, decision) {
  const id = button.dataset.assistantApprove || button.dataset.assistantReject;
  const done = setButtonBusy(button, decision === "approve" ? "Executing" : "Rejecting");
  try {
    await api(`/assistant/actions/${id}/${decision}`, { method: "POST", body: "{}" });
    ui.toast(decision === "approve" ? "Assistant action executed" : "Assistant action rejected", "success");
    render();
  } catch (err) {
    ui.toast(err.message, "error");
  } finally {
    done();
  }
}

function selectedReaderSelection() {
  const reader = document.querySelector(".reader-content");
  const selection = window.getSelection ? window.getSelection() : null;
  if (!reader || !selection || selection.rangeCount === 0) return null;
  const range = selection.getRangeAt(0);
  const start = range.startContainer.nodeType === Node.ELEMENT_NODE ? range.startContainer : range.startContainer.parentElement;
  const end = range.endContainer.nodeType === Node.ELEMENT_NODE ? range.endContainer : range.endContainer.parentElement;
  const quote = selection.toString().replace(/\s+/g, " ").trim().slice(0, 4000);
  if (!start || !end || !reader.contains(start) || !reader.contains(end) || !quote) return null;
  return { quote, range };
}

function selectedReaderText() {
  return selectedReaderSelection()?.quote || "";
}

function readerQuoteSelector(quote) {
  const reader = document.querySelector(".reader-content");
  const exact = String(quote || "").replace(/\s+/g, " ").trim().slice(0, 4000);
  if (!reader || !exact) return {};
  const readerText = (reader.textContent || "").replace(/\s+/g, " ").trim();
  const offset = readerText.indexOf(exact);
  return {
    type: "TextQuoteSelector",
    exact,
    prefix: offset > 0 ? readerText.slice(Math.max(0, offset - 80), offset) : "",
    suffix: offset >= 0 ? readerText.slice(offset + exact.length, offset + exact.length + 80) : "",
    offset,
  };
}

function bindReaderAnnotationComposer(bookmarkID) {
  const reader = document.querySelector(".reader-content");
  if (!reader) return;
  let composer = null;

  const closeComposer = (restoreFocus = false) => {
    if (!composer) return;
    composer.remove();
    composer = null;
    if (restoreFocus) reader.focus({ preventScroll: true });
  };
  const openComposer = () => {
    const selection = selectedReaderSelection();
    if (!selection) {
      closeComposer();
      return;
    }
    if (composer?.dataset.quote === selection.quote) return;
    closeComposer();
    const rect = selection.range.getBoundingClientRect();
    composer = document.createElement("section");
    composer.className = "reader-annotation-composer";
    composer.dataset.quote = selection.quote;
    composer.setAttribute("role", "dialog");
    composer.setAttribute("aria-label", "Annotate selected passage");
    composer.innerHTML = `<form class="form">
      <p class="meta">Selected passage</p>
      <blockquote class="reader-annotation-quote"></blockquote>
      <div class="field"><label for="reader-annotation-note">Your note</label><textarea id="reader-annotation-note" rows="3" placeholder="Why this matters"></textarea></div>
      <p class="form-message" aria-live="polite" hidden></p>
      <p class="button-row"><button type="button" class="secondary" data-reader-annotation-cancel>Cancel</button><button type="submit">Save annotation</button></p>
    </form>`;
    composer.querySelector(".reader-annotation-quote").textContent = selection.quote;
    const width = Math.min(352, window.innerWidth - 32);
    composer.style.left = `${Math.max(16, Math.min(rect.left, window.innerWidth - width - 16))}px`;
    composer.style.top = `${Math.min(window.innerHeight - 16, Math.max(16, rect.bottom + 12))}px`;
    document.body.append(composer);
    if (composer.getBoundingClientRect().bottom > window.innerHeight - 16) {
      composer.style.top = `${Math.max(16, rect.top - composer.offsetHeight - 12)}px`;
    }
    const form = composer.querySelector("form");
    const note = composer.querySelector("#reader-annotation-note");
    const message = composer.querySelector(".form-message");
    composer.querySelector("[data-reader-annotation-cancel]").addEventListener("click", () => closeComposer(true));
    form.addEventListener("submit", async (event) => {
      event.preventDefault();
      const save = form.querySelector("button[type=submit]");
      const done = setButtonBusy(save, "Saving annotation");
      message.hidden = true;
      try {
        await api(`/bookmarks/${bookmarkID}/annotations`, {
          method: "POST",
          body: JSON.stringify({ quote: selection.quote, note: note.value, tags: [], selector: readerQuoteSelector(selection.quote) }),
        });
        ui.toast("Annotation saved", "success");
        closeComposer();
        render();
      } catch (err) {
        message.textContent = err.message;
        message.hidden = false;
        ui.toast(err.message, "error");
      } finally {
        done();
      }
    });
    requestAnimationFrame(() => note.focus());
  };
  const scheduleComposer = () => requestAnimationFrame(openComposer);

  ui.on(reader, "pointerup", scheduleComposer);
  ui.on(reader, "keyup", (event) => {
    if (event.shiftKey || event.key === "Shift") scheduleComposer();
  });
  ui.on(document, "pointerdown", (event) => {
    if (composer && !composer.contains(event.target) && !reader.contains(event.target)) closeComposer();
  });
  ui.on(document, "keydown", (event) => {
    if (composer && event.key === "Escape") {
      event.preventDefault();
      closeComposer(true);
    }
  });
  state.cleanup.push(() => closeComposer());
}

function findReaderTextRange(reader, quote) {
  const exact = String(quote || "").replace(/\s+/g, " ").trim();
  if (!reader || !exact || !document.createTreeWalker) return null;
  const walker = document.createTreeWalker(reader, NodeFilter.SHOW_TEXT);
  let node;
  while ((node = walker.nextNode())) {
    const index = node.nodeValue.indexOf(exact);
    if (index < 0) continue;
    const range = document.createRange();
    range.setStart(node, index);
    range.setEnd(node, index + exact.length);
    return range;
  }
  return null;
}

function clearReaderJumpHighlights() {
  document.querySelectorAll("mark.reader-jump-highlight").forEach((mark) => mark.replaceWith(document.createTextNode(mark.textContent || "")));
}

function jumpToReaderQuote(quote) {
  const reader = document.querySelector(".reader-content");
  clearReaderJumpHighlights();
  const range = findReaderTextRange(reader, quote);
  if (!range) {
    reader?.scrollIntoView({ behavior: "smooth", block: "start" });
    ui.toast("Quote not found in the archived reader text.", "info");
    return;
  }
  const mark = document.createElement("mark");
  mark.className = "reader-jump-highlight";
  try {
    range.surroundContents(mark);
    mark.scrollIntoView({ behavior: "smooth", block: "center" });
  } catch {
    reader?.scrollIntoView({ behavior: "smooth", block: "start" });
  }
}

async function showJobStatus(jobID) {
  const status = document.querySelector("#job-status");
  if (!jobID || !status) return;
  status.hidden = false;
  status.textContent = "Queued for archiving";
  for (let i = 0; i < 60; i += 1) {
    await new Promise((resolve) => setTimeout(resolve, 1000));
    const job = await api(`/jobs/${jobID}`).catch(() => null);
    if (!job) return;
    status.innerHTML = jobStatusMarkup(job.status);
    if (job.status === "completed" || job.status === "failed") {
      if (location.pathname.startsWith("/bookmark/")) render();
      return;
    }
  }
}

function jobStatusLabel(status) {
  if (status === "queued") return "Queued for archiving";
  if (status === "leased" || status === "running" || status === "processing") return "Fetching and summarizing";
  if (status === "completed") return "Saved and enriched";
  if (status === "failed") return "Processing failed. Open import jobs or server logs for details.";
  return status ? `Processing: ${status.replaceAll("_", " ")}` : "Processing saved item";
}

function jobStatusMarkup(status) {
  if (status === "failed") {
    return `Processing failed. <a class="text-link" href="/settings">Open import jobs</a> or check server logs.`;
  }
  return escapeHTML(jobStatusLabel(status));
}

function tagList(tags) {
  if (!tags.length) return "";
  return `<div class="chips">${tags.map((tag) => `<span>${escapeHTML(tag.name || tag)}</span>`).join("")}</div>`;
}

function summaryPanel(summary) {
  const bullets = Array.isArray(summary.bullet_points) ? summary.bullet_points : [];
  const highlights = Array.isArray(summary.highlights) ? summary.highlights : [];
  const tags = Array.isArray(summary.suggested_tags) ? summary.suggested_tags : [];
  const longForm = typeof summary.long_form === "string" ? summary.long_form.trim() : "";
  const hasSummary = summary.one_sentence || longForm || bullets.length || highlights.length || tags.length;
  const statusNotice = {
    partial: `<p><strong>Partial extraction:</strong> the source returned too little readable article content. Reprocess after checking that the page is publicly accessible.</p>`,
    pending: `<p><strong>Reprocessing:</strong> the existing archive remains visible until refreshed content is ready.</p>`,
    failed: `<p><strong>Reprocessing failed:</strong> the existing archive is unchanged. Check source access or server logs before retrying.</p>`,
  }[summary.processing_status] || "";
  const summaryContent = hasSummary ? `
    ${summary.one_sentence ? `<p>${escapeHTML(summary.one_sentence)}</p>` : ""}
    ${longForm ? `<p class="meta">Executive summary</p><div class="summary-long-form">${longForm.split(/\n\s*\n/).map((paragraph) => `<p>${escapeHTML(paragraph)}</p>`).join("")}</div>` : ""}
    ${bullets.length ? `<p class="meta">Key points</p><ul>${bullets.map((item) => `<li>${escapeHTML(item)}</li>`).join("")}</ul>` : ""}
    ${highlights.length ? `<p class="meta">Highlights</p><ul>${highlights.map((item) => `<li>${escapeHTML(item)}</li>`).join("")}</ul>` : ""}
    ${tags.length ? `<div class="chips">${tags.map((tag) => `<span>${escapeHTML(tag)}</span>`).join("")}</div>` : ""}` : "";
  return `<section class="insight-strip">
    <span class="meta">${hasSummary ? "Summary" : "Enrichment"}</span>
    ${statusNotice || (!hasSummary ? `<p>${escapeHTML(summary.processing_status || "Queued")}</p>` : "")}
    ${summaryContent}
  </section>`;
}

function annotationList(items) {
  if (!items.length) return `<p class="meta">No annotations yet.</p>`;
  return `<div class="stack">${items.map((item) => `<article class="annotation form" data-annotation="${escapeHTML(item.id)}">
    <div class="field"><label for="annotation-quote-${escapeHTML(item.id)}">Quote</label><textarea id="annotation-quote-${escapeHTML(item.id)}" data-annotation-quote rows="3">${escapeHTML(item.quote || "")}</textarea></div>
    <div class="field"><label for="annotation-note-${escapeHTML(item.id)}">Note</label><textarea id="annotation-note-${escapeHTML(item.id)}" data-annotation-note rows="3">${escapeHTML(item.note || "")}</textarea></div>
    <div class="field"><label for="annotation-tags-${escapeHTML(item.id)}">Tags</label><input id="annotation-tags-${escapeHTML(item.id)}" data-annotation-tags value="${escapeHTML((item.tags || []).join(", "))}"></div>
    <p class="button-row">
      <button type="button" class="secondary" data-annotation-jump="${escapeHTML(item.id)}">Jump to source</button>
      <button type="button" data-annotation-save="${escapeHTML(item.id)}">Save changes</button>
      <button type="button" class="danger" data-annotation-delete="${escapeHTML(item.id)}">Delete annotation</button>
    </p>
  </article>`).join("")}</div>`;
}

function noteList(items) {
  if (!items.length) return `<p class="meta">No linked notes yet.</p>`;
  return `<div class="stack">${items.map((item) => `<article class="annotation">
    <h3>${escapeHTML(item.title || "Untitled note")}</h3>
    <p>${escapeHTML(item.body || "")}</p>
  </article>`).join("")}</div>`;
}

function relatedList(items) {
  if (!items.length) return `<p class="meta">Related items appear after enrichment has enough semantic data.</p>`;
  return `<div class="grid compact-grid">${items.map((item) => `<a class="panel bookmark" href="/bookmark/${escapeHTML(item.id)}">
    <span class="meta">${escapeHTML(item.domain || "web")} · ${Number(item.similarity_score || 0).toFixed(2)}</span>
    <h3>${escapeHTML(item.title || item.url)}</h3>
    <p>${escapeHTML(item.description || "")}</p>
  </a>`).join("")}</div>`;
}

async function notesPage() {
  await requireUser();
  const focusedID = new URLSearchParams(location.search).get("note");
  if (focusedID) {
    navigate(`/notes/${encodeURIComponent(focusedID)}`, true);
    return;
  }
  const detailMatch = location.pathname.match(/^\/notes\/([^/]+)$/);
  if (detailMatch) {
    await noteDetailPage(decodeURIComponent(detailMatch[1]));
    return;
  }
  const result = await api("/notes");
  const notes = result.notes || [];
  setRoot(shell("Notes", `
    <section class="split">
      <form class="panel form" id="standalone-note-form">
        <h2>New note</h2>
        <div class="field"><label for="standalone-note-title">Title</label><input id="standalone-note-title" type="text" placeholder="Idea, decision, or snippet"></div>
        <div class="field"><label for="standalone-note-body">Body</label><textarea id="standalone-note-body" rows="6" placeholder="Write the thought before it disappears"></textarea>${voiceButton("standalone-note-body", "new note")}</div>
        <p class="form-message" id="standalone-note-message" data-form-message hidden></p>
        <button type="submit">Save note</button>
      </form>
      <section class="panel">
        <span class="meta">Standalone memory</span>
        <h2>${notes.length} notes</h2>
        <p>Use notes for ideas, snippets, and context that should be searchable even when it is not tied to a URL.</p>
      </section>
    </section>
    <section class="stack">
      ${notes.map(noteListItem).join("") || emptyState({ eyebrow: "No notes", title: "Start with one thought", body: "Standalone notes can be linked to bookmarks later through the reader workflow." })}
    </section>
  `));
  const form = document.querySelector("#standalone-note-form");
  bindVoiceCapture();
  form.addEventListener("submit", async (event) => {
    event.preventDefault();
    const done = setButtonBusy(event.submitter, "Saving note");
    setFormMessage(form);
    try {
      await api("/notes", {
        method: "POST",
        body: JSON.stringify({
          title: document.querySelector("#standalone-note-title").value,
          body: document.querySelector("#standalone-note-body").value,
        }),
      });
      ui.toast("Note saved", "success");
      render();
    } catch (err) {
      setFormMessage(form, err.message);
      ui.toast(err.message, "error");
    } finally {
      done();
    }
  });
}

function noteListItem(note) {
  const state = note.item_state || {};
  return `<a class="panel bookmark" href="/notes/${encodeURIComponent(note.id)}">
    <span class="meta">${escapeHTML(state.stage || "inbox")} · ${escapeHTML(formatDate(note.updated_at))}</span>
    <h2>${escapeHTML(note.title || "Untitled note")}</h2>
    <p>${escapeHTML((note.body || "").slice(0, 220))}</p>
  </a>`;
}

async function noteDetailPage(id) {
  const [note, noteTargets, bookmarkTargets] = await Promise.all([
    api(`/notes/${encodeURIComponent(id)}`),
    api("/link-targets?type=note&limit=100").catch(() => ({ targets: [] })),
    api("/link-targets?type=bookmark&limit=100").catch(() => ({ targets: [] })),
  ]);
  setRoot(shell(note.title || "Note", `
    ${standaloneNoteCard(note, noteTargets.targets || [], bookmarkTargets.targets || [])}
  `));
  document.querySelectorAll("[data-note-save]").forEach((button) => {
    button.addEventListener("click", () => updateStandaloneNote(button));
  });
  document.querySelectorAll("[data-note-delete]").forEach((button) => {
    button.addEventListener("click", () => deleteStandaloneNote(button));
  });
  bindNoteLinkForms();
  bindNoteBookmarkLinkForms();
  bindLinkDeleteControls();
  bindActionItemControls();
  bindReminderControls();
  bindVoiceCapture();
}

function standaloneNoteCard(note, notes, bookmarks = []) {
  return `<article class="panel form" data-note="${escapeHTML(note.id)}">
    <div class="field"><label for="note-title-${escapeHTML(note.id)}">Title</label><input id="note-title-${escapeHTML(note.id)}" data-note-title value="${escapeHTML(note.title || "")}"></div>
    <div class="field"><label for="note-body-${escapeHTML(note.id)}">Body</label><textarea id="note-body-${escapeHTML(note.id)}" data-note-body rows="12">${escapeHTML(note.body || "")}</textarea>${voiceButton(`note-body-${note.id}`, "note body")}</div>
    <p class="meta">${note.bookmark_id ? `Linked to bookmark ${escapeHTML(note.bookmark_id)}` : "Standalone"} · ${escapeHTML(note.updated_at || "")}</p>
    <p class="button-row">
      ${note.bookmark_id ? `<a class="button secondary" href="/bookmark/${escapeHTML(note.bookmark_id)}">Open bookmark</a>` : ""}
      <a class="button secondary" href="/notes">All notes</a>
      <button type="button" data-note-save="${escapeHTML(note.id)}">Save changes</button>
      <button type="button" class="danger" data-note-delete="${escapeHTML(note.id)}">Delete note</button>
    </p>
    <section>
      <h3>Action items</h3>
      ${actionItemsPanel("note", note.id, note.action_items || [])}
    </section>
    <section>
      <h3>Reminder</h3>
      ${reminderForm("note", note.id)}
      ${reminderList(note.reminders || [])}
    </section>
    <section>
      <h3>Links</h3>
      ${noteLinkForm(note, notes)}
      ${noteBookmarkLinkForm(note, bookmarks)}
      ${linkList(note.links || {})}
    </section>
  </article>`;
}

async function updateStandaloneNote(button) {
  const card = button.closest("[data-note]");
  const done = setButtonBusy(button, "Saving");
  try {
    await api(`/notes/${button.dataset.noteSave}`, {
      method: "PATCH",
      body: JSON.stringify({
        title: card.querySelector("[data-note-title]").value,
        body: card.querySelector("[data-note-body]").value,
      }),
    });
    ui.toast("Note updated", "success");
    render();
  } catch (err) {
    ui.toast(err.message, "error");
  } finally {
    done();
  }
}

async function deleteStandaloneNote(button) {
  const confirmed = await ui.confirmDestructive({ title: "Delete note", body: "This removes the note and any bookmark link to it.", confirm: "Delete", cancel: "Keep note" });
  if (!confirmed) return;
  const done = setButtonBusy(button, "Deleting");
  try {
    await api(`/notes/${button.dataset.noteDelete}`, { method: "DELETE" });
    ui.toast("Note deleted", "success");
    navigate("/notes", true);
  } catch (err) {
    ui.toast(err.message, "error");
  } finally {
    done();
  }
}

const objectFieldSets = {
  project: [{ key: "status", label: "Status", type: "select", options: ["active", "paused", "complete"] }, { key: "outcome", label: "Desired outcome" }],
  person: [{ key: "role", label: "Role or relationship" }, { key: "contact", label: "Contact note" }],
  book: [{ key: "author", label: "Author" }, { key: "status", label: "Reading status", type: "select", options: ["to read", "reading", "finished"] }],
  meeting: [{ key: "date", label: "Meeting date", type: "datetime-local" }, { key: "attendees", label: "Attendees" }],
  decision: [{ key: "decision", label: "Decision" }, { key: "rationale", label: "Rationale" }],
  research_thread: [{ key: "question", label: "Research question" }, { key: "status", label: "Status", type: "select", options: ["open", "developing", "resolved"] }],
};

function objectFieldsMarkup(type) {
  const fields = objectFieldSets[type] || [];
  return fields.map((field) => `<div class="field"><label for="object-field-${field.key}">${escapeHTML(field.label)}</label>${field.type === "select" ? `<select id="object-field-${field.key}" data-object-field="${field.key}">${field.options.map((option) => `<option value="${escapeHTML(option)}">${escapeHTML(knowledgeTypeLabel(option))}</option>`).join("")}</select>` : `<input id="object-field-${field.key}" data-object-field="${field.key}" type="${field.type || "text"}">`}</div>`).join("");
}

function collectObjectFields(root = document) {
  return Object.fromEntries([...root.querySelectorAll("[data-object-field]")].map((field) => [field.dataset.objectField, field.value.trim()]).filter(([, value]) => value));
}

async function openObjectComposer() {
  const body = document.createElement("div");
  body.innerHTML = `<form class="form" id="object-composer-form">
    <div class="field"><label for="object-composer-type">Type</label><select id="object-composer-type">${objectTypeOptions(Object.keys(objectFieldSets), "project")}</select></div>
    <div class="field"><label for="object-composer-title">Title</label><input id="object-composer-title" required></div>
    <div class="field"><label for="object-composer-description">Description</label><textarea id="object-composer-description" rows="4"></textarea></div>
    <fieldset class="object-fields"><legend>Details</legend><div id="object-composer-fields"></div></fieldset>
    <p class="form-message" data-form-message hidden></p>
    <button type="submit">Create object</button>
  </form>`;
  const form = body.querySelector("form");
  const type = body.querySelector("#object-composer-type");
  const renderFields = () => { body.querySelector("#object-composer-fields").innerHTML = objectFieldsMarkup(type.value); };
  type.addEventListener("change", renderFields);
  renderFields();
  form.addEventListener("submit", async (event) => {
    event.preventDefault();
    const done = setButtonBusy(event.submitter, "Creating");
    try {
      await api("/objects", { method: "POST", body: JSON.stringify({ object_type: type.value, title: body.querySelector("#object-composer-title").value, description: body.querySelector("#object-composer-description").value, fields: collectObjectFields(body) }) });
      document.querySelector("[data-dialog-close]")?.click();
      ui.toast("Object created", "success");
      navigate("/library?type=knowledge_object", true);
    } catch (err) {
      setFormMessage(form, err.message);
    } finally {
      done();
    }
  });
  await ui.dialog({ title: "New object", body, actions: [{ label: "Cancel", value: false, kind: "secondary" }] });
}

function objectTypeOptions(types, selected) {
  return types.map((type) => `<option value="${escapeHTML(type)}"${type === selected ? " selected" : ""}>${escapeHTML(type.replaceAll("_", " "))}</option>`).join("");
}

async function evolutionPage() {
  await requireUser();
  const query = new URLSearchParams(location.search).get("q") || "";
  const result = query ? await api(`/evolution?q=${encodeURIComponent(query)}`) : { timeline: [] };
  setRoot(shell("Evolution", `
    <form class="panel form" id="evolution-form">
      <h2>Topic evolution</h2>
      <div class="field"><label for="evolution-query">Topic or phrase</label><input id="evolution-query" type="search" value="${escapeHTML(query)}" placeholder="Roadmap, pricing, local-first"></div>
      <button type="submit">Trace topic</button>
    </form>
    <section class="stack">
      ${(result.timeline || []).map(evolutionItem).join("") || emptyState({ eyebrow: "No timeline yet", title: "Search a topic", body: "Arivu will line up matching daily notes, saved pages, notes, decisions, meetings, and projects.", tag: "article" })}
    </section>
  `));
  document.querySelector("#evolution-form").addEventListener("submit", (event) => {
    event.preventDefault();
    const value = document.querySelector("#evolution-query").value.trim();
    navigate(value ? `/evolution?q=${encodeURIComponent(value)}` : "/evolution", true);
  });
}

function evolutionItem(item) {
  return `<article class="annotation">
    <p><strong>${escapeHTML(item.title || "Untitled")}</strong> <span class="meta">${escapeHTML(item.item_type || "item")} · ${escapeHTML(item.updated_at || "")}</span></p>
    <p>${escapeHTML(item.body || "")}</p>
    ${item.href ? `<p><a class="text-link" href="${escapeHTML(item.href)}">Open source</a></p>` : ""}
  </article>`;
}

async function boardPage() {
  await requireUser();
  const board = await api("/today-board");
  setRoot(shell("Board", `<div class="home-view board-view">
    ${homeViewTabs("board")}
    <div class="board-scroller" role="region" aria-label="Knowledge workflow board" tabindex="0">
      <section class="board-grid">
        ${(board.columns || []).map(boardColumn).join("")}
      </section>
    </div>
  </div>`, { wide: true }));
}

function boardColumn(column) {
  const items = column.items || [];
  return `<section class="panel board-column">
    <header class="board-column-header"><span class="meta">${items.length} items</span><h2>${escapeHTML(column.title || "Column")}</h2></header>
    <div class="stack board-column-items">${items.map(boardItem).join("") || `<p class="meta">Nothing here.</p>`}</div>
  </section>`;
}

function boardItem(item) {
  const href = item.href || (item.item_type === "note" ? `/notes/${encodeURIComponent(item.id)}` : item.item_type === "bookmark" ? `/bookmark/${encodeURIComponent(item.id)}` : "/objects");
  return `<article class="annotation compact-object board-item">
    <p><strong>${escapeHTML(item.title || "Untitled")}</strong></p>
    <p class="meta">${escapeHTML(item.item_type || item.object_type || "object")} ${item.next_action ? `· ${escapeHTML(item.next_action)}` : ""}</p>
    <p>${escapeHTML(item.description || item.body || "")}</p>
    <a class="text-link" href="${escapeHTML(href)}">Open</a>
  </article>`;
}

async function bookmarkPage() {
  await requireUser();
  const id = location.pathname.split("/").pop();
  const [bookmark, related, noteTargets] = await Promise.all([
    api(`/bookmarks/${id}`),
    api(`/bookmarks/${id}/related?limit=4`).catch(() => ({ related: [] })),
    api("/link-targets?type=note&limit=100").catch(() => ({ targets: [] })),
  ]);
  const summary = bookmark.ai_summary || {};
  const itemState = bookmark.item_state || { stage: "inbox", importance: 0, next_action: "" };
  const artifacts = bookmark.artifacts || [];
  setRoot(shell(bookmark.title || "Bookmark", `
    <article class="panel reader primary-reader">
      <p class="meta">${bookmark.domain || ""} · ${bookmark.reading_time || 0} min</p>
      <p class="meta" role="status">Capture: ${escapeHTML((bookmark.capture_status || "saved").replaceAll("_", " "))}</p>
      <p class="button-row">
        <a class="button" href="${escapeHTML(bookmark.url)}" target="_blank" rel="noreferrer noopener">Open original</a>
        <button type="button" class="secondary" id="toggle-read">${bookmark.read_status ? "Mark unread" : "Mark read"}</button>
        <button type="button" class="secondary" id="reprocess-bookmark">Reprocess</button>
        <button type="button" class="danger" id="delete-bookmark">Delete bookmark</button>
      </p>
      <p id="job-status" hidden></p>
      <div class="reading-progress"><label for="reading-progress">Reading progress</label><progress id="reading-progress" max="100" value="${Math.round(Number(bookmark.reading_progress || 0) * 100)}"></progress><span id="reading-progress-value">${Math.round(Number(bookmark.reading_progress || 0) * 100)}%</span></div>
      ${tagList(bookmark.tags || [])}
      ${summaryPanel(summary)}
      <div class="reader-content" tabindex="-1">${bookmark.html_content || `<p>${escapeHTML(bookmark.text_content || bookmark.description || "No archived text yet.")}</p>`}</div>
      ${preservationPanel(artifacts, bookmark.capture_status || "saved")}
      ${evidencePanel(bookmark.evidence || [])}
      <details><summary>Capture attempt history (${(bookmark.capture_attempts || []).length})</summary><ul>${(bookmark.capture_attempts || []).map((attempt) => `<li><strong>${escapeHTML(attempt.status)}</strong> · ${escapeHTML(attempt.engine)} ${attempt.error_code ? `· ${escapeHTML(attempt.error_code)}` : ""}</li>`).join("")}</ul></details>
      <details class="panel"><summary>Share this bookmark</summary><form id="share-bookmark-form" class="form"><div class="field"><label for="share-title">Public title</label><input id="share-title" required maxlength="200" value="${escapeHTML(bookmark.title || "Shared bookmark")}"></div><div class="field"><label for="share-expiry">Expires (optional)</label><input id="share-expiry" type="datetime-local"></div><button type="submit">Create share link</button><p class="form-message" data-form-message hidden></p><div id="share-created" hidden></div></form></details>
    </article>
    <section class="panel primary-work">
      <div>
        <p class="meta">Next step</p>
        <h2>Decide what this becomes</h2>
      </div>
      ${processingStrip(id, itemState)}
      <p class="button-row">
        <button type="button" class="secondary" id="review-complete">Mark review done</button>
      </p>
    </section>
    <details class="panel disclosure-panel workbench-group">
      <summary><span>Capture passages</span><span class="meta">${(bookmark.annotations || []).length} saved</span></summary>
      <section class="split disclosure-body embedded-split">
        <form class="form" id="annotation-form">
          <h2>New annotation</h2>
          <div class="field"><label for="annotation-quote">Quote</label><textarea id="annotation-quote" rows="3" placeholder="Paste the passage worth keeping"></textarea></div>
          <div class="field"><label for="annotation-note">Note</label><textarea id="annotation-note" rows="4" placeholder="Your interpretation, decision, or next action"></textarea></div>
          <div class="field"><label for="annotation-tags">Tags</label><input id="annotation-tags" type="text" placeholder="strategy, quote"></div>
          <p class="form-message" id="annotation-message" data-form-message hidden></p>
          <div class="button-row">
            <button type="button" class="secondary" id="use-selection">Use selected text</button>
            <button type="submit">Save annotation</button>
          </div>
        </form>
        <section>
          <h2>Saved annotations</h2>
          ${annotationList(bookmark.annotations || [])}
        </section>
      </section>
    </details>
    <details class="panel disclosure-panel workbench-group">
      <summary><span>Notes and links</span><span class="meta">${(bookmark.notes || []).length} notes</span></summary>
      <section class="split disclosure-body embedded-split">
        <form class="form" id="note-form">
          <h2>Linked note</h2>
          <div class="field"><label for="note-title">Title</label><input id="note-title" type="text" placeholder="Working note"></div>
          <div class="field"><label for="note-body">Body</label><textarea id="note-body" rows="5" placeholder="Turn this saved item into usable knowledge"></textarea>${voiceButton("note-body", "linked note")}</div>
          <p class="form-message" id="note-message" data-form-message hidden></p>
          <button type="submit">Save note</button>
        </form>
        <section>
          <h2>Linked notes</h2>
          ${noteList(bookmark.notes || [])}
        </section>
      </section>
      <section class="split disclosure-body embedded-split">
        <form class="form" id="link-form">
          <h2>Connect a note</h2>
          <div class="field"><label for="link-note">Note</label><select id="link-note">${noteOptions(noteTargets.targets || [], bookmark.notes || [])}</select></div>
          <div class="field"><label for="link-label">Label</label><input id="link-label" type="text" maxlength="80" placeholder="supports, contradicts, next step"></div>
          <p class="form-message" id="link-message" data-form-message hidden></p>
          <button type="submit">Create link</button>
        </form>
        <section>
          <h2>Connections</h2>
          ${linkList(bookmark.links || {})}
        </section>
      </section>
    </details>
    <details class="panel disclosure-panel workbench-group">
      <summary><span>Tasks and reminders</span><span class="meta">${(bookmark.action_items || []).length} tasks · ${(bookmark.reminders || []).length} reminders</span></summary>
      <section class="split disclosure-body embedded-split">
        <form class="form" data-action-item-form data-item-type="bookmark" data-item-id="${escapeHTML(id)}">
          <h2>Action item</h2>
          <div class="field"><label for="action-item-title">Task</label><input id="action-item-title" data-action-item-title type="text" maxlength="300" placeholder="Concrete thing to do with this"></div>
          <p class="form-message" data-form-message hidden></p>
          <button type="submit">Add task</button>
        </form>
        <section>
          <h2>Action items</h2>
          ${actionItemsList(bookmark.action_items || [])}
        </section>
      </section>
      <section class="split disclosure-body embedded-split">
        ${reminderForm("bookmark", id)}
        <section>
          <h2>Reminders</h2>
          ${reminderList(bookmark.reminders || [])}
        </section>
      </section>
    </details>
    <details class="panel disclosure-panel workbench-group">
      <summary><span>Related items</span><span class="meta">${(related.related || []).length} matches</span></summary>
      <section class="disclosure-body">
        ${relatedList(related.related || [])}
      </section>
    </details>
  `));
  bindPreservedPageViewers();
  const reader = document.querySelector(".reader-content");
  let progressTimer;
  const saveProgress = () => {
    const max = Math.max(1, reader.scrollHeight - reader.clientHeight);
    const progress = Math.max(0, Math.min(1, reader.scrollTop / max));
    document.querySelector("#reading-progress").value = Math.round(progress * 100);
    document.querySelector("#reading-progress-value").textContent = `${Math.round(progress * 100)}%`;
    clearTimeout(progressTimer);
    progressTimer = setTimeout(() => api(`/bookmarks/${id}/reading-progress`, { method: "PUT", body: JSON.stringify({ progress }) }).catch(() => {}), 500);
  };
  reader?.addEventListener("scroll", saveProgress, { passive: true });
  const shareForm = document.querySelector("#share-bookmark-form");
  shareForm?.addEventListener("submit", async (event) => {
    event.preventDefault(); const done = setButtonBusy(event.submitter, "Creating"); setFormMessage(shareForm);
    try {
      const expiry = document.querySelector("#share-expiry").value;
      const result = await api("/shares", { method: "POST", body: JSON.stringify({ title: document.querySelector("#share-title").value, item_ids: [id], expires_at: expiry ? new Date(expiry).toISOString() : null }) });
      const box = document.querySelector("#share-created"); box.hidden = false; box.innerHTML = `<p><strong>Copy this link now.</strong> The token is shown only once.</p><p class="button-row"><input readonly value="${escapeHTML(new URL(result.url, location.origin).href)}"><button type="button" class="secondary" id="copy-share">Copy</button></p>`;
      box.querySelector("#copy-share").addEventListener("click", () => navigator.clipboard.writeText(box.querySelector("input").value).then(() => ui.toast("Share link copied", "success")));
    } catch (err) { setFormMessage(shareForm, err.message); } finally { done(); }
  });
  document.querySelector("#toggle-read").addEventListener("click", async (event) => {
    const done = setButtonBusy(event.currentTarget, bookmark.read_status ? "Marking unread" : "Marking read");
    try {
      await api(`/bookmarks/${id}/read-status`, { method: "PATCH", body: JSON.stringify({ read_status: !bookmark.read_status }) });
      ui.toast("Read status updated", "success");
      render();
    } catch (err) {
      ui.toast(err.message, "error");
    } finally {
      done();
    }
  });
  document.querySelector("#reprocess-bookmark").addEventListener("click", async (event) => {
    const done = setButtonBusy(event.currentTarget, "Queueing");
    try {
      const result = await api(`/bookmarks/${id}/reprocess`, { method: "POST", body: "{}" });
      ui.toast("Bookmark queued for reprocessing", "success");
      showJobStatus(result.job_id);
    } catch (err) {
      ui.toast(err.message, "error");
    } finally {
      done();
    }
  });
  document.querySelector("#review-complete").addEventListener("click", async (event) => {
    const done = setButtonBusy(event.currentTarget, "Completing review");
    try {
      await api(`/review/bookmark:${id}/complete`, { method: "POST", body: "{}" });
      ui.toast("Review completed", "success");
      render();
    } catch (err) {
      ui.toast(err.message, "error");
    } finally {
      done();
    }
  });
  document.querySelectorAll("[data-reader-stage]").forEach((button) => {
    button.addEventListener("click", () => updateReaderState(button, button.dataset.readerStage));
  });
  document.querySelector("#processing-save").addEventListener("click", (event) => updateReaderState(event.currentTarget, document.querySelector("#processing-stage").value));
  bindPriorityButtons();
  bindReaderAnnotationComposer(id);
  const annotationForm = document.querySelector("#annotation-form");
  document.querySelector("#use-selection").addEventListener("click", () => {
    const quote = selectedReaderText();
    if (!quote) {
      setFormMessage(annotationForm, "Select text inside the reader first.");
      return;
    }
    document.querySelector("#annotation-quote").value = quote;
    setFormMessage(annotationForm, "Selected text copied into the quote field.", "success");
  });
  annotationForm.addEventListener("submit", async (event) => {
    event.preventDefault();
    const done = setButtonBusy(event.submitter, "Saving annotation");
    setFormMessage(annotationForm);
    try {
      await api(`/bookmarks/${id}/annotations`, {
        method: "POST",
        body: JSON.stringify({
          quote: document.querySelector("#annotation-quote").value,
          note: document.querySelector("#annotation-note").value,
          tags: splitTags(document.querySelector("#annotation-tags").value),
          selector: readerQuoteSelector(document.querySelector("#annotation-quote").value),
        }),
      });
      ui.toast("Annotation saved", "success");
      render();
    } catch (err) {
      setFormMessage(annotationForm, err.message);
      ui.toast(err.message, "error");
    } finally {
      done();
    }
  });
  const noteForm = document.querySelector("#note-form");
  noteForm.addEventListener("submit", async (event) => {
    event.preventDefault();
    const done = setButtonBusy(event.submitter, "Saving note");
    setFormMessage(noteForm);
    try {
      await api("/notes", {
        method: "POST",
        body: JSON.stringify({
          bookmark_id: id,
          title: document.querySelector("#note-title").value,
          body: document.querySelector("#note-body").value,
        }),
      });
      ui.toast("Note saved", "success");
      render();
    } catch (err) {
      setFormMessage(noteForm, err.message);
      ui.toast(err.message, "error");
    } finally {
      done();
    }
  });
  const linkForm = document.querySelector("#link-form");
  linkForm.addEventListener("submit", async (event) => {
    event.preventDefault();
    const noteID = document.querySelector("#link-note").value;
    if (!noteID) {
      setFormMessage(linkForm, "Choose a note to link.");
      return;
    }
    const done = setButtonBusy(event.submitter, "Linking");
    setFormMessage(linkForm);
    try {
      await api("/links", {
        method: "POST",
        body: JSON.stringify({
          from_type: "bookmark",
          from_id: id,
          to_type: "note",
          to_id: noteID,
          label: document.querySelector("#link-label").value,
        }),
      });
      ui.toast("Link created", "success");
      render();
    } catch (err) {
      setFormMessage(linkForm, err.message);
      ui.toast(err.message, "error");
    } finally {
      done();
    }
  });
  bindLinkDeleteControls();
  bindActionItemControls();
  bindReminderControls();
  bindVoiceCapture();
  document.querySelector("#delete-bookmark").addEventListener("click", async () => {
    const confirmed = await ui.confirmDestructive({ title: "Delete bookmark", body: "This removes the bookmark, summary, graph terms, and collection links.", confirm: "Delete bookmark", cancel: "Keep bookmark" });
    if (!confirmed) return;
    try {
      await api(`/bookmarks/${id}`, { method: "DELETE" });
      ui.toast("Bookmark deleted", "success");
      navigate("/dashboard", true);
    } catch (err) {
      ui.toast(err.message, "error");
    }
  });
  document.querySelectorAll("[data-annotation-save]").forEach((button) => {
    button.addEventListener("click", () => updateAnnotation(button));
  });
  document.querySelectorAll("[data-annotation-jump]").forEach((button) => {
    button.addEventListener("click", () => jumpToReaderQuote(button.closest("[data-annotation]")?.querySelector("[data-annotation-quote]")?.value || ""));
  });
  document.querySelectorAll("[data-annotation-delete]").forEach((button) => {
    button.addEventListener("click", () => deleteAnnotation(button));
  });
}

function reminderList(reminders) {
  if (!reminders.length) return `<p class="meta">No reminders set.</p>`;
  return `<div class="stack">${reminders.map((reminder) => `<article class="annotation">
    <p><strong>${escapeHTML(formatDate(reminder.due_at))}</strong> <span class="meta">${reminderMeta(reminder)}</span></p>
    ${reminder.note ? `<p>${escapeHTML(reminder.note)}</p>` : ""}
    <p class="button-row">
      ${reminder.status === "completed" ? "" : `<button type="button" class="secondary" data-reminder-snooze="${escapeHTML(reminder.id)}" data-minutes="30">30m</button><button type="button" class="secondary" data-reminder-snooze="${escapeHTML(reminder.id)}" data-days="1">Tomorrow</button>`}
      ${reminder.status === "completed" ? "" : `<button type="button" data-reminder-complete="${escapeHTML(reminder.id)}">Complete</button>`}
      <button type="button" class="danger" data-reminder-delete="${escapeHTML(reminder.id)}">Delete reminder</button>
    </p>
    ${reminder.status === "completed" ? "" : reminderEditForm(reminder)}
  </article>`).join("")}</div>`;
}

function reminderForm(itemType, itemID, layout = "inline") {
  const dueID = `reminder-due-${itemType}-${itemID}`;
  const noteID = `reminder-note-${itemType}-${itemID}`;
  const recurrenceID = `reminder-recurrence-${itemType}-${itemID}`;
  const intervalID = `reminder-interval-${itemType}-${itemID}`;
  const channelID = `reminder-channel-${itemType}-${itemID}`;
  const className = layout === "panel" ? "panel form" : "task-form";
  return `<form class="${className}" data-reminder-form data-item-type="${escapeHTML(itemType)}" data-item-id="${escapeHTML(itemID)}">
    ${layout === "panel" ? "<h2>Reminder</h2>" : ""}
    <div class="field"><label for="${escapeHTML(dueID)}">Due</label><input id="${escapeHTML(dueID)}" data-reminder-due type="datetime-local" required></div>
    <input data-reminder-timezone type="hidden" value="${escapeHTML(browserTimezone())}">
    <div class="field"><label for="${escapeHTML(recurrenceID)}">Repeat</label><select id="${escapeHTML(recurrenceID)}" data-reminder-recurrence>${reminderRecurrenceOptions("none")}</select></div>
    <div class="field"><label for="${escapeHTML(intervalID)}">Custom days</label><input id="${escapeHTML(intervalID)}" data-reminder-interval type="number" min="1" max="365" inputmode="numeric" placeholder="Only for custom"></div>
    <div class="field"><label for="${escapeHTML(channelID)}">Notify</label><select id="${escapeHTML(channelID)}" data-reminder-channel>${reminderChannelOptions("in_app")}</select></div>
    <div class="field"><label for="${escapeHTML(noteID)}">Note</label><input id="${escapeHTML(noteID)}" data-reminder-note type="text" maxlength="500" placeholder="Why this should come back"></div>
    <p class="form-message" data-form-message hidden></p>
    <button type="submit" class="secondary">Set reminder</button>
  </form>`;
}

function reminderEditForm(reminder) {
  const id = escapeHTML(reminder.id || "");
  return `<details class="inline-details">
    <summary>Edit reminder</summary>
    <form class="task-form" data-reminder-edit="${id}">
      <input data-reminder-timezone type="hidden" value="${escapeHTML(reminder.timezone || browserTimezone())}">
      <label class="sr-only" for="edit-reminder-due-${id}">Due</label>
      <input id="edit-reminder-due-${id}" data-reminder-due type="datetime-local" value="${escapeHTML(rfc3339ToLocalDateTime(reminder.due_at))}" required>
      <label class="sr-only" for="edit-reminder-recur-${id}">Repeat</label>
      <select id="edit-reminder-recur-${id}" data-reminder-recurrence>${reminderRecurrenceOptions(reminder.recurrence || "none")}</select>
      <label class="sr-only" for="edit-reminder-interval-${id}">Custom days</label>
      <input id="edit-reminder-interval-${id}" data-reminder-interval type="number" min="1" max="365" inputmode="numeric" value="${reminder.recurrence === "custom" ? escapeHTML(String(reminder.recurrence_interval_days || "")) : ""}" placeholder="Custom days">
      <label class="sr-only" for="edit-reminder-channel-${id}">Notify</label>
      <select id="edit-reminder-channel-${id}" data-reminder-channel>${reminderChannelOptions(reminder.notification_channel || "in_app")}</select>
      <label class="sr-only" for="edit-reminder-note-${id}">Note</label>
      <input id="edit-reminder-note-${id}" data-reminder-note type="text" maxlength="500" value="${escapeHTML(reminder.note || "")}" placeholder="Why this should come back">
      <p class="form-message" data-form-message hidden></p>
      <button type="submit" class="secondary">Save reminder</button>
    </form>
  </details>`;
}

function reminderRecurrenceOptions(current) {
  return [["none", "Once"], ["daily", "Daily"], ["weekly", "Weekly"], ["monthly", "Monthly"], ["custom", "Custom days"]].map(([value, label]) => `<option value="${value}" ${value === current ? "selected" : ""}>${label}</option>`).join("");
}

function reminderChannelOptions(current) {
  return [["in_app", "In-app"], ["email", "In-app + email"]].map(([value, label]) => `<option value="${value}" ${value === current ? "selected" : ""}>${label}</option>`).join("");
}

function reminderMeta(reminder) {
  const parts = [reminder.due_state || reminder.status || "pending", reminder.item_type || "item", reminder.item_title || ""];
  if (reminder.recurrence && reminder.recurrence !== "none") parts.push(reminder.recurrence === "custom" ? `every ${reminder.recurrence_interval_days || "?"}d` : reminder.recurrence);
  if (reminder.notification_channel === "email") parts.push("email");
  return escapeHTML(parts.filter(Boolean).join(" · "));
}

function actionItemsPanel(itemType, itemID, items) {
  return `<section class="task-panel">
    <form class="task-form" data-action-item-form data-item-type="${escapeHTML(itemType)}" data-item-id="${escapeHTML(itemID)}">
      <label class="sr-only" for="task-${escapeHTML(itemType)}-${escapeHTML(itemID)}">Action item</label>
      <input id="task-${escapeHTML(itemType)}-${escapeHTML(itemID)}" data-action-item-title type="text" maxlength="300" placeholder="Add a task for this item">
      <button type="submit" class="secondary">Add task</button>
      <p class="form-message" data-form-message hidden></p>
    </form>
    ${actionItemsList(items)}
  </section>`;
}

function actionItemsList(items) {
  if (!items.length) return `<p class="meta">No action items yet.</p>`;
  return `<div class="stack">${items.map((item) => `<article class="annotation">
    <p><strong>${escapeHTML(item.title || "Action item")}</strong> <span class="meta">${escapeHTML(item.status || "pending")} · ${escapeHTML(item.item_title || "")}</span></p>
    <p class="button-row">
      ${item.status === "completed" ? "" : `<button type="button" data-action-item-complete="${escapeHTML(item.id)}">Complete</button>`}
      <button type="button" class="danger" data-action-item-delete="${escapeHTML(item.id)}">Delete task</button>
    </p>
  </article>`).join("")}</div>`;
}

function bindActionItemControls() {
  document.querySelectorAll("[data-action-item-form]").forEach((form) => {
    form.addEventListener("submit", submitActionItem);
  });
  document.querySelectorAll("[data-action-item-complete]").forEach((button) => {
    button.addEventListener("click", () => completeActionItem(button));
  });
  document.querySelectorAll("[data-action-item-delete]").forEach((button) => {
    button.addEventListener("click", () => deleteActionItem(button));
  });
}

function bindReminderControls() {
  document.querySelectorAll("[data-reminder-form]").forEach((form) => {
    form.addEventListener("submit", submitReminder);
  });
  document.querySelectorAll("[data-reminder-edit]").forEach((form) => {
    form.addEventListener("submit", submitReminderEdit);
  });
  document.querySelectorAll("[data-reminder-snooze]").forEach((button) => {
    button.addEventListener("click", () => snoozeReminder(button));
  });
  document.querySelectorAll("[data-reminder-complete]").forEach((button) => {
    button.addEventListener("click", () => completeReminder(button));
  });
  document.querySelectorAll("[data-reminder-delete]").forEach((button) => {
    button.addEventListener("click", () => deleteReminder(button));
  });
}

async function submitActionItem(event) {
  event.preventDefault();
  const form = event.currentTarget;
  const done = setButtonBusy(event.submitter, "Adding");
  setFormMessage(form);
  try {
    await api("/action-items", {
      method: "POST",
      body: JSON.stringify({
        item_type: form.dataset.itemType,
        item_id: form.dataset.itemId,
        title: form.querySelector("[data-action-item-title]").value,
      }),
    });
    ui.toast("Action item added", "success");
    render();
  } catch (err) {
    setFormMessage(form, err.message);
    ui.toast(err.message, "error");
  } finally {
    done();
  }
}

async function submitReminder(event) {
  event.preventDefault();
  const form = event.currentTarget;
  const dueAt = localDateTimeToRFC3339(form.querySelector("[data-reminder-due]").value);
  if (!dueAt) {
    setFormMessage(form, "Choose a valid reminder time.");
    return;
  }
  const done = setButtonBusy(event.submitter, "Saving reminder");
  setFormMessage(form);
  try {
    await api("/reminders", {
      method: "POST",
      body: JSON.stringify(reminderPayload(form, {
        item_type: form.dataset.itemType,
        item_id: form.dataset.itemId,
        due_at: dueAt,
      })),
    });
    ui.toast("Reminder set", "success");
    render();
  } catch (err) {
    setFormMessage(form, err.message);
    ui.toast(err.message, "error");
  } finally {
    done();
  }
}

async function submitReminderEdit(event) {
  event.preventDefault();
  const form = event.currentTarget;
  const dueAt = localDateTimeToRFC3339(form.querySelector("[data-reminder-due]").value);
  if (!dueAt) {
    setFormMessage(form, "Choose a valid reminder time.");
    return;
  }
  const done = setButtonBusy(event.submitter, "Saving");
  setFormMessage(form);
  try {
    await api(`/reminders/${form.dataset.reminderEdit}`, {
      method: "PATCH",
      body: JSON.stringify(reminderPayload(form, { due_at: dueAt })),
    });
    ui.toast("Reminder updated", "success");
    render();
  } catch (err) {
    setFormMessage(form, err.message);
    ui.toast(err.message, "error");
  } finally {
    done();
  }
}

function reminderPayload(form, base) {
  const recurrence = form.querySelector("[data-reminder-recurrence]")?.value || "none";
  const intervalValue = Number(form.querySelector("[data-reminder-interval]")?.value || 0);
  const channel = form.querySelector("[data-reminder-channel]")?.value || "in_app";
  return {
    ...base,
    timezone: form.querySelector("[data-reminder-timezone]")?.value || browserTimezone(),
    recurrence,
    recurrence_interval_days: recurrence === "custom" ? intervalValue : 0,
    notification_channel: channel,
    email_enabled: channel === "email",
    note: form.querySelector("[data-reminder-note]")?.value || "",
  };
}

async function completeActionItem(button) {
  const done = setButtonBusy(button, "Completing");
  try {
    await api(`/action-items/${button.dataset.actionItemComplete}/complete`, { method: "POST", body: "{}" });
    ui.toast("Action item completed", "success");
    render();
  } catch (err) {
    ui.toast(err.message, "error");
  } finally {
    done();
  }
}

async function deleteActionItem(button) {
  const confirmed = await ui.confirmDestructive({ title: "Delete action item", body: "This removes only the task.", confirm: "Delete task", cancel: "Keep task" });
  if (!confirmed) return;
  const done = setButtonBusy(button, "Deleting");
  try {
    await api(`/action-items/${button.dataset.actionItemDelete}`, { method: "DELETE" });
    ui.toast("Action item deleted", "success");
    render();
  } catch (err) {
    ui.toast(err.message, "error");
  } finally {
    done();
  }
}

function localDateTimeToRFC3339(value) {
  if (!value) return "";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "";
  return date.toISOString();
}

function rfc3339ToLocalDateTime(value) {
  if (!value) return "";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "";
  const offset = date.getTimezoneOffset() * 60000;
  return new Date(date.getTime() - offset).toISOString().slice(0, 16);
}

function browserTimezone() {
  return Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC";
}

async function snoozeReminder(button) {
  const body = {};
  if (button.dataset.minutes) body.minutes = Number(button.dataset.minutes);
  if (button.dataset.days) body.days = Number(button.dataset.days);
  const done = setButtonBusy(button, "Snoozing");
  try {
    await api(`/reminders/${button.dataset.reminderSnooze}/snooze`, { method: "POST", body: JSON.stringify(body) });
    ui.toast("Reminder snoozed", "success");
    render();
  } catch (err) {
    ui.toast(err.message, "error");
  } finally {
    done();
  }
}

async function completeReminder(button) {
  const done = setButtonBusy(button, "Completing");
  try {
    await api(`/reminders/${button.dataset.reminderComplete}/complete`, { method: "POST", body: "{}" });
    ui.toast("Reminder completed", "success");
    render();
  } catch (err) {
    ui.toast(err.message, "error");
  } finally {
    done();
  }
}

async function deleteReminder(button) {
  const confirmed = await ui.confirmDestructive({ title: "Delete reminder", body: "This removes the reminder only.", confirm: "Delete reminder", cancel: "Keep reminder" });
  if (!confirmed) return;
  const done = setButtonBusy(button, "Deleting");
  try {
    await api(`/reminders/${button.dataset.reminderDelete}`, { method: "DELETE" });
    ui.toast("Reminder deleted", "success");
    render();
  } catch (err) {
    ui.toast(err.message, "error");
  } finally {
    done();
  }
}

function noteOptions(notes, linkedNotes) {
  const linked = new Set((linkedNotes || []).map((note) => note.id));
  const available = (notes || []).filter((note) => !linked.has(note.id));
  if (!available.length) return `<option value="">No standalone notes available</option>`;
  return `<option value="">Choose note</option>${available.map((note) => `<option value="${escapeHTML(note.id)}">${escapeHTML(note.title || "Untitled note")}</option>`).join("")}`;
}

function noteLinkOptions(notes, note) {
  const linked = new Set(((note.links || {}).outgoing || []).filter((link) => link.to_type === "note").map((link) => link.to_id));
  const available = (notes || []).filter((candidate) => candidate.id !== note.id && !linked.has(candidate.id));
  if (!available.length) return `<option value="">No other notes available</option>`;
  return `<option value="">Choose note</option>${available.map((candidate) => `<option value="${escapeHTML(candidate.id)}">${escapeHTML(candidate.title || "Untitled note")}</option>`).join("")}`;
}

function noteLinkForm(note, notes) {
  return `<form class="task-form" data-note-link-form data-from-id="${escapeHTML(note.id)}">
    <label class="sr-only" for="note-link-target-${escapeHTML(note.id)}">Linked note</label>
    <select id="note-link-target-${escapeHTML(note.id)}" data-link-target>${noteLinkOptions(notes, note)}</select>
    <label class="sr-only" for="note-link-label-${escapeHTML(note.id)}">Link label</label>
    <input id="note-link-label-${escapeHTML(note.id)}" data-link-label type="text" maxlength="80" placeholder="supports, contradicts, follows">
    <button type="submit" class="secondary">Link note</button>
    <p class="form-message" data-form-message hidden></p>
  </form>`;
}

function linkList(links) {
  const outgoing = links.outgoing || [];
  const incoming = links.incoming || [];
  if (!outgoing.length && !incoming.length) return `<p class="meta">No explicit links yet.</p>`;
  return `<div class="stack">
    ${outgoing.map((link) => linkCard(link, "To")).join("")}
    ${incoming.map((link) => linkCard(link, "From")).join("")}
  </div>`;
}

function linkCard(link, prefix) {
  const targetType = prefix === "To" ? link.to_type : link.from_type;
  const targetID = prefix === "To" ? link.to_id : link.from_id;
  const title = prefix === "To" ? link.to_title : link.from_title;
  const href = targetType === "note" ? `/notes/${encodeURIComponent(targetID)}` : `/bookmark/${escapeHTML(targetID)}`;
  return `<article class="annotation">
    <p><strong>${prefix} ${escapeHTML(title || targetID)}</strong> <span class="meta">${escapeHTML(link.label || "linked")} · ${escapeHTML(targetType)}</span></p>
    <p class="button-row">
      <a class="button secondary" href="${href}">Open</a>
      <button type="button" class="danger" data-link-delete="${escapeHTML(link.id)}">Delete link</button>
    </p>
  </article>`;
}

async function deleteLink(button) {
  const confirmed = await ui.confirmDestructive({ title: "Delete link", body: "This removes only the explicit relationship, not either item.", confirm: "Delete link", cancel: "Keep link" });
  if (!confirmed) return;
  const done = setButtonBusy(button, "Deleting");
  try {
    await api(`/links/${button.dataset.linkDelete}`, { method: "DELETE" });
    ui.toast("Link deleted", "success");
    render();
  } catch (err) {
    ui.toast(err.message, "error");
  } finally {
    done();
  }
}

function bindLinkDeleteControls() {
  document.querySelectorAll("[data-link-delete]").forEach((button) => {
    button.addEventListener("click", () => deleteLink(button));
  });
}

function bindNoteLinkForms() {
  document.querySelectorAll("[data-note-link-form]").forEach((form) => {
    form.addEventListener("submit", submitNoteLink);
  });
}

function bindNoteBookmarkLinkForms() {
  document.querySelectorAll("[data-note-bookmark-link-form]").forEach((form) => {
    form.addEventListener("submit", submitNoteBookmarkLink);
  });
}

async function submitNoteLink(event) {
  event.preventDefault();
  const form = event.currentTarget;
  const targetID = form.querySelector("[data-link-target]").value;
  if (!targetID) {
    setFormMessage(form, "Choose a note to link.");
    return;
  }
  const done = setButtonBusy(event.submitter, "Linking");
  setFormMessage(form);
  try {
    await api("/links", {
      method: "POST",
      body: JSON.stringify({
        from_type: "note",
        from_id: form.dataset.fromId,
        to_type: "note",
        to_id: targetID,
        label: form.querySelector("[data-link-label]").value,
      }),
    });
    ui.toast("Link created", "success");
    render();
  } catch (err) {
    setFormMessage(form, err.message);
    ui.toast(err.message, "error");
  } finally {
    done();
  }
}

function noteBookmarkLinkForm(note, bookmarks) {
  const linked = new Set(((note.links || {}).outgoing || []).filter((link) => link.to_type === "bookmark").map((link) => link.to_id));
  const available = (bookmarks || []).filter((bookmark) => bookmark.id && !linked.has(bookmark.id));
  if (!available.length) return "";
  return `<form class="task-form" data-note-bookmark-link-form data-from-id="${escapeHTML(note.id)}">
    <label class="sr-only" for="note-bookmark-link-target-${escapeHTML(note.id)}">Linked bookmark</label>
    <select id="note-bookmark-link-target-${escapeHTML(note.id)}" data-link-target>
      <option value="">Choose bookmark</option>
      ${available.map((bookmark) => `<option value="${escapeHTML(bookmark.id)}">${escapeHTML(bookmark.title || bookmark.url || "Untitled bookmark")}</option>`).join("")}
    </select>
    <label class="sr-only" for="note-bookmark-link-label-${escapeHTML(note.id)}">Link label</label>
    <input id="note-bookmark-link-label-${escapeHTML(note.id)}" data-link-label type="text" maxlength="80" placeholder="supports, cites, relates">
    <button type="submit" class="secondary">Link bookmark</button>
    <p class="form-message" data-form-message hidden></p>
  </form>`;
}

async function submitNoteBookmarkLink(event) {
  event.preventDefault();
  const form = event.currentTarget;
  const targetID = form.querySelector("[data-link-target]").value;
  if (!targetID) {
    setFormMessage(form, "Choose a bookmark to link.");
    return;
  }
  const done = setButtonBusy(event.submitter, "Linking");
  setFormMessage(form);
  try {
    await api("/links", {
      method: "POST",
      body: JSON.stringify({
        from_type: "note",
        from_id: form.dataset.fromId,
        to_type: "bookmark",
        to_id: targetID,
        label: form.querySelector("[data-link-label]").value,
      }),
    });
    ui.toast("Bookmark linked", "success");
    render();
  } catch (err) {
    setFormMessage(form, err.message);
    ui.toast(err.message, "error");
  } finally {
    done();
  }
}

function processingStrip(bookmarkID, itemState) {
  return `<section class="insight-strip processing-strip" data-reader-item="bookmark:${escapeHTML(bookmarkID)}">
    <span class="meta">Workflow · ${readerStageLabel(itemState.stage || "inbox")}</span>
    <div class="split compact-split">
      <div class="field">
        <label for="processing-next-action">Next action</label>
        <input id="processing-next-action" data-next-action value="${escapeHTML(itemState.next_action || "")}" maxlength="500" placeholder="One concrete follow-up">
      </div>
      <fieldset class="field priority-field">
        <legend>Priority</legend>
        <input data-importance type="hidden" value="${Number(itemState.importance || 0)}">
        <div class="priority-buttons">${priorityButtons(itemState.importance || 0)}</div>
      </fieldset>
      <div class="field">
        <label for="processing-stage">Stage</label>
        <select id="processing-stage" data-next-stage>
          ${["inbox", "processing", "processed", "archived"].map((stage) => `<option value="${stage}" ${stage === itemState.stage ? "selected" : ""}>${readerStageLabel(stage)}</option>`).join("")}
        </select>
      </div>
    </div>
    <p class="button-row">
      <button type="button" id="processing-save">Save next step</button>
      <button type="button" class="secondary" data-reader-stage="processed">Keep</button>
      <button type="button" class="secondary" data-reader-stage="archived">Archive</button>
    </p>
  </section>`;
}

function readerStageLabel(stage) {
  return escapeHTML(({ inbox: "Inbox", processing: "Working", processed: "Kept", archived: "Archived" })[stage] || stage || "Inbox");
}

async function updateReaderState(button, stage) {
  const card = button.closest("[data-reader-item]");
  const done = setButtonBusy(button, "Saving");
  try {
    await saveItemState(card.dataset.readerItem, stage, Number(card.querySelector("[data-importance]").value || 0), card.querySelector("[data-next-action]").value);
    ui.toast("Processing state updated", "success");
    render();
  } catch (err) {
    ui.toast(err.message, "error");
  } finally {
    done();
  }
}

async function updateAnnotation(button) {
  const card = button.closest("[data-annotation]");
  const done = setButtonBusy(button, "Saving");
  try {
    await api(`/annotations/${button.dataset.annotationSave}`, {
      method: "PATCH",
      body: JSON.stringify({
        quote: card.querySelector("[data-annotation-quote]").value,
        note: card.querySelector("[data-annotation-note]").value,
        tags: splitTags(card.querySelector("[data-annotation-tags]").value),
        selector: readerQuoteSelector(card.querySelector("[data-annotation-quote]").value),
      }),
    });
    ui.toast("Annotation updated", "success");
    render();
  } catch (err) {
    ui.toast(err.message, "error");
  } finally {
    done();
  }
}

async function deleteAnnotation(button) {
  const confirmed = await ui.confirmDestructive({ title: "Delete annotation", body: "This removes the saved quote and note from this bookmark.", confirm: "Delete annotation", cancel: "Keep annotation" });
  if (!confirmed) return;
  const done = setButtonBusy(button, "Deleting");
  try {
    await api(`/annotations/${button.dataset.annotationDelete}`, { method: "DELETE" });
    ui.toast("Annotation deleted", "success");
    render();
  } catch (err) {
    ui.toast(err.message, "error");
  } finally {
    done();
  }
}

async function settingsPage() {
  const user = await requireUser();
  const requestedSection = new URLSearchParams(location.search).get("section") || "profile";
  const tabs = [
    ["profile", "Profile", "Manage your profile and account access."],
    ["import", "Import", "Bring in browser, Pocket, Raindrop, or URL-list exports."],
    ["collections", "Collections", "Create, rename, move, and delete Library collections."],
    ["tags", "Tags", "Keep tag names consistent and merge aliases into one tag."],
    ["connections", "Connections", "Connect provider accounts and sync saved items."],
    ["automation", "Automation", "Control AI tagging and RSS capture."],
    ["sharing", "Sharing", "Review and revoke public links."],
  ];
  if (user.is_admin) tabs.push(["api-keys", "Provider settings", "Configure optional AI, email, and X connections."]);
  if (requestedSection === "api-keys" && !user.is_admin) history.replaceState({}, "", "/settings?section=profile");
  const active = tabs.some(([id]) => id === requestedSection) ? requestedSection : "profile";
  setRoot(shell("Settings", `<section class="tabs" id="settings-tabs">
    <div class="tab-list" role="tablist" aria-label="Settings sections">
      ${tabs.map(([id, label]) => `<button type="button" role="tab" id="tab-${id}" aria-controls="panel-${id}" aria-selected="${id === active}">${label}</button>`).join("")}
    </div>
    ${tabs.map(([id, label, copy]) => `<div role="tabpanel" id="panel-${id}" aria-labelledby="tab-${id}"><h2>${label}</h2><p>${copy}</p>${settingsPanel(id)}</div>`).join("")}
  </section>`));
  ui.tabs(document.querySelector("#settings-tabs"));
  bindProfilePanel();
  bindImportPanel();
  bindCollectionSettingsPanel();
  bindTagSettingsPanel();
  bindConnectionsPanel();
  bindAutomationPanel();
  bindSharingPanel();
  if (user.is_admin) bindAPIKeysPanel();
}

function settingsPanel(id) {
  if (id === "profile") return profilePanel();
  if (id === "import") return importPanel();
  if (id === "collections") return collectionSettingsPanel();
  if (id === "tags") return tagSettingsPanel();
  if (id === "connections") return connectionsPanel();
  if (id === "automation") return `<section class="split"><form class="panel form" id="ai-tagging-form"><h3>AI tagging</h3><div class="field"><label for="ai-tagging-mode">Mode</label><select id="ai-tagging-mode"><option value="off">Off</option><option value="existing-vocabulary">Existing vocabulary only</option><option value="allow-new">Allow new tags</option></select></div><p class="form-message" id="ai-tagging-message" data-form-message hidden></p><button type="submit">Save mode</button></form><form class="panel form" id="feed-form"><h3>Add RSS or Atom feed</h3><div class="field"><label for="feed-url">Feed URL</label><input id="feed-url" type="url" required></div><div class="field"><label for="feed-name">Name</label><input id="feed-name"></div><div class="field"><label for="feed-tags">Tags</label><input id="feed-tags" placeholder="news, research"></div><p class="form-message" id="feed-message" data-form-message hidden></p><button type="submit">Add subscription</button></form></section><section class="panel"><h3 id="feed-list-heading" tabindex="-1">Subscriptions</h3><p class="form-message" id="feed-list-message" role="alert" hidden></p><div id="feed-list"></div></section>`;
  if (id === "sharing") return `<section class="panel"><h3 id="share-list-heading" tabindex="-1">Public links</h3><p>Tokens are shown only when a link is created.</p><p class="form-message" id="share-list-message" role="alert" hidden></p><div id="share-list"></div></section>`;
  if (id === "api-keys") return apiKeysPanel();
  return "";
}

async function bindAutomationPanel() {
  const mode = document.querySelector("#ai-tagging-mode"), form = document.querySelector("#ai-tagging-form"), feedForm = document.querySelector("#feed-form"); if (!form) return;
  try { mode.value = (await api("/ai-tagging")).mode; } catch (err) { setFormMessage(form, err.message); }
  form.addEventListener("submit", async (event) => { event.preventDefault(); const done = setButtonBusy(event.submitter, "Saving"); setFormMessage(form); try { await api("/ai-tagging", { method: "PUT", body: JSON.stringify({ mode: mode.value }) }); setFormMessage(form, "AI tagging mode saved.", "success"); } catch (err) { setFormMessage(form, err.message); } finally { done(); } });
  const refresh = async (focusID = "", focusHeading = false) => { const message = document.querySelector("#feed-list-message"); try { const feeds = await api("/subscriptions"); message.hidden = true; document.querySelector("#feed-list").innerHTML = feeds.map(f => `<article class="annotation"><p><strong>${escapeHTML(f.name || f.url)}</strong> <span class="meta">${escapeHTML(f.status)}${f.last_poll_at ? ` · checked ${escapeHTML(f.last_poll_at)}` : ""}</span></p>${f.error ? `<p>${escapeHTML(f.error)}</p>` : ""}<p class="button-row"><button class="secondary" data-feed-toggle="${escapeHTML(f.id)}" data-enabled="${f.enabled}">${f.enabled ? "Pause" : "Resume"}</button><button class="danger" data-feed-delete="${escapeHTML(f.id)}">Delete</button></p></article>`).join("") || `<p class="meta">No subscriptions yet.</p>`; document.querySelectorAll("[data-feed-toggle]").forEach(b => b.onclick = async () => { const id = b.dataset.feedToggle; const done = setButtonBusy(b, b.dataset.enabled === "true" ? "Pausing" : "Resuming"); try { await api(`/subscriptions/${id}`, { method: "PATCH", body: JSON.stringify({ enabled: b.dataset.enabled !== "true" }) }); await refresh(id); } catch (err) { message.textContent = err.message; message.hidden = false; } finally { done(); } }); document.querySelectorAll("[data-feed-delete]").forEach(b => b.onclick = async () => { if (await ui.confirmDestructive({ title: "Delete subscription", body: "Captured bookmarks remain in your library.", confirm: "Delete", cancel: "Keep" })) { const done = setButtonBusy(b, "Deleting"); try { await api(`/subscriptions/${b.dataset.feedDelete}`, { method: "DELETE" }); await refresh("", true); } catch (err) { message.textContent = err.message; message.hidden = false; } finally { done(); } } }); if (focusID) [...document.querySelectorAll("[data-feed-toggle]")].find((button) => button.dataset.feedToggle === focusID)?.focus(); else if (focusHeading) document.querySelector("#feed-list-heading")?.focus(); } catch (err) { message.textContent = err.message; message.hidden = false; } };
  feedForm.addEventListener("submit", async event => { event.preventDefault(); const done = setButtonBusy(event.submitter, "Adding"); setFormMessage(feedForm); try { await api("/subscriptions", { method: "POST", body: JSON.stringify({ url: document.querySelector("#feed-url").value, name: document.querySelector("#feed-name").value, tags: splitTags(document.querySelector("#feed-tags").value) }) }); feedForm.reset(); await refresh(); setFormMessage(feedForm, "Subscription added.", "success"); } catch (err) { setFormMessage(feedForm, err.message); } finally { done(); } }); await refresh();
}

async function bindSharingPanel() { const list = document.querySelector("#share-list"), message = document.querySelector("#share-list-message"); if (!list) return; const refresh = async (focusHeading = false) => { try { const shares = await api("/shares"); message.hidden = true; list.innerHTML = shares.map(s => `<article class="annotation"><p><strong>${escapeHTML(s.title)}</strong> <span class="meta">${s.revoked_at ? "Revoked" : s.expires_at ? `Expires ${escapeHTML(s.expires_at)}` : "Active"}</span></p>${!s.revoked_at ? `<button class="danger" data-revoke-share="${escapeHTML(s.id)}">Revoke</button>` : ""}</article>`).join("") || `<p class="meta">No public links.</p>`; document.querySelectorAll("[data-revoke-share]").forEach(b => b.onclick = async () => { if (await ui.confirmDestructive({ title: "Revoke public link", body: "Anyone using this link will immediately lose access.", confirm: "Revoke", cancel: "Keep active" })) { const done = setButtonBusy(b, "Revoking"); try { await api(`/shares/${b.dataset.revokeShare}/revoke`, { method: "POST", body: "{}" }); await refresh(true); } catch (err) { message.textContent = err.message; message.hidden = false; } finally { done(); } } }); if (focusHeading) document.querySelector("#share-list-heading")?.focus(); } catch (err) { message.textContent = err.message; message.hidden = false; } }; await refresh(); }

function profilePanel() {
  return `<section class="split">
    <form class="panel form" id="profile-form">
      <h3>Profile</h3>
      <div class="field"><label for="profile-email">Email</label><input id="profile-email" type="email" disabled data-skip-form-message></div>
      <div class="field"><label for="profile-name">Name</label><input id="profile-name" type="text" autocomplete="name"></div>
      <p class="form-message" id="profile-message" data-form-message hidden></p>
      <button type="submit">Save profile</button>
    </form>
    <form class="panel form" id="password-form">
      <h3>Password</h3>
      <div class="field"><label for="current-password">Current password</label><input id="current-password" type="password" autocomplete="current-password"></div>
      <div class="field"><label for="new-password">New password</label><input id="new-password" type="password" autocomplete="new-password" minlength="8"></div>
      <p class="form-message" id="password-message" data-form-message hidden></p>
      <button type="submit">Change password</button>
    </form>
  </section>`;
}

async function bindProfilePanel() {
  const profileForm = document.querySelector("#profile-form");
  const passwordForm = document.querySelector("#password-form");
  if (!profileForm || !passwordForm) return;
  try {
    const profile = await api("/user/profile");
    document.querySelector("#profile-email").value = profile.email || "";
    document.querySelector("#profile-name").value = profile.name || "";
  } catch (err) {
    setFormMessage(profileForm, err.message);
  }
  profileForm.addEventListener("submit", async (event) => {
    event.preventDefault();
    const done = setButtonBusy(event.submitter, "Saving");
    setFormMessage(profileForm);
    try {
      state.user = await api("/user/profile", { method: "PUT", body: JSON.stringify({ name: document.querySelector("#profile-name").value }) });
      ui.toast("Profile saved", "success");
    } catch (err) {
      setFormMessage(profileForm, err.message);
      ui.toast(err.message, "error");
    } finally {
      done();
    }
  });
  passwordForm.addEventListener("submit", async (event) => {
    event.preventDefault();
    const done = setButtonBusy(event.submitter, "Changing");
    setFormMessage(passwordForm);
    try {
      await api("/auth/change-password", {
        method: "POST",
        body: JSON.stringify({
          current_password: document.querySelector("#current-password").value,
          new_password: document.querySelector("#new-password").value,
        }),
      });
      passwordForm.reset();
      ui.toast("Password changed", "success");
    } catch (err) {
      setFormMessage(passwordForm, err.message);
      ui.toast(err.message, "error");
    } finally {
      done();
    }
  });
}

function collectionSettingsPanel() {
  return `<section class="split collection-settings">
    <form class="panel form" id="collection-form">
      <h3>New collection</h3>
      <div class="field"><label for="collection-name">Name</label><input id="collection-name" required maxlength="120" placeholder="Research"></div>
      <div class="field"><label for="collection-parent">Parent</label><select id="collection-parent"><option value="">No parent</option></select></div>
      <p class="form-message" id="collection-message" data-form-message hidden></p>
      <button type="submit">Create collection</button>
    </form>
    <section class="panel" aria-labelledby="collection-list-heading">
      <h3 id="collection-list-heading" tabindex="-1">Current collections</h3>
      <p class="form-message" id="collection-list-message" role="alert" hidden></p>
      <div id="collection-list" class="stack"><p class="meta">Loading collections.</p></div>
    </section>
  </section>`;
}

async function bindCollectionSettingsPanel() {
  const form = document.querySelector("#collection-form");
  const list = document.querySelector("#collection-list");
  const parent = document.querySelector("#collection-parent");
  const listMessage = document.querySelector("#collection-list-message");
  if (!form || !list || !parent) return;
  const refresh = async (focusID = "", focusHeading = false) => {
    try {
      const collections = await api("/collections");
      listMessage.hidden = true;
      parent.innerHTML = `<option value="">No parent</option>${collections.map((item) => `<option value="${escapeHTML(item.id)}">${escapeHTML(item.name)}</option>`).join("")}`;
      list.innerHTML = collections.map((item) => { const descendants = collectionDescendantIDs(collections, item.id); return `<form class="collection-editor annotation" data-collection-id="${escapeHTML(item.id)}">
        <div class="field"><label for="collection-name-${escapeHTML(item.id)}">Name</label><input id="collection-name-${escapeHTML(item.id)}" data-collection-name value="${escapeHTML(item.name)}" required maxlength="120"></div>
        <div class="field"><label for="collection-parent-${escapeHTML(item.id)}">Parent</label><select id="collection-parent-${escapeHTML(item.id)}" data-collection-parent><option value="">No parent</option>${collections.filter((candidate) => candidate.id !== item.id && !descendants.has(candidate.id)).map((candidate) => `<option value="${escapeHTML(candidate.id)}"${candidate.id === item.parent_id ? " selected" : ""}>${escapeHTML(candidate.name)}</option>`).join("")}</select></div>
        <p class="form-message" data-form-message hidden></p>
        <div class="button-row"><button type="submit" class="secondary">Save</button><button type="button" class="danger" data-collection-delete>Delete</button></div>
      </form>`; }).join("") || `<p class="meta">No collections yet. Create one to organize related saves.</p>`;
      list.querySelectorAll(".collection-editor").forEach((editor) => {
        editor.addEventListener("submit", async (event) => {
          event.preventDefault();
          const done = setButtonBusy(event.submitter, "Saving");
          setFormMessage(editor);
          try {
            await api(`/collections/${encodeURIComponent(editor.dataset.collectionId)}`, { method: "PATCH", body: JSON.stringify({ name: editor.querySelector("[data-collection-name]").value, parent_id: editor.querySelector("[data-collection-parent]").value }) });
            await refresh(editor.dataset.collectionId);
          } catch (err) { setFormMessage(editor, err.message); } finally { done(); }
        });
        editor.querySelector("[data-collection-delete]").addEventListener("click", async (event) => {
          const confirmed = await ui.confirmDestructive({ title: "Delete collection", body: "Bookmarks stay in your Library. Child collections must be moved or deleted first.", confirm: "Delete collection", cancel: "Keep collection" });
          if (!confirmed) return;
          const done = setButtonBusy(event.currentTarget, "Deleting");
          setFormMessage(editor);
          try { await api(`/collections/${encodeURIComponent(editor.dataset.collectionId)}`, { method: "DELETE" }); await refresh("", true); } catch (err) { setFormMessage(editor, err.message); } finally { done(); }
        });
      });
      if (focusID) [...list.querySelectorAll(".collection-editor")].find((editor) => editor.dataset.collectionId === focusID)?.querySelector("[data-collection-name]")?.focus();
      else if (focusHeading) document.querySelector("#collection-list-heading")?.focus();
    } catch (err) {
      listMessage.textContent = err.message;
      listMessage.hidden = false;
    }
  };
  form.addEventListener("submit", async (event) => {
    event.preventDefault();
    const done = setButtonBusy(event.submitter, "Creating");
    setFormMessage(form);
    try {
      await api("/collections", { method: "POST", body: JSON.stringify({ name: document.querySelector("#collection-name").value, parent_id: parent.value }) });
      form.reset();
      await refresh();
      setFormMessage(form, "Collection created.", "success");
    } catch (err) { setFormMessage(form, err.message); } finally { done(); }
  });
  await refresh();
}

function collectionDescendantIDs(collections, rootID) {
  const descendants = new Set();
  let changed = true;
  while (changed) {
    changed = false;
    for (const item of collections) {
      if (!descendants.has(item.id) && (item.parent_id === rootID || descendants.has(item.parent_id))) {
        descendants.add(item.id);
        changed = true;
      }
    }
  }
  return descendants;
}

function tagSettingsPanel() {
  return `<section class="split">
    <form class="panel form" id="tag-form">
      <h3>Primary tag</h3>
      <div class="field"><label for="tag-name">Name</label><input id="tag-name" type="text" placeholder="Research"></div>
      <p class="form-message" id="tag-message" data-form-message hidden></p>
      <button type="submit">Create tag</button>
    </form>
    <form class="panel form" id="tag-alias-form">
      <h3>Alias</h3>
      <div class="field"><label for="alias-tag">Primary tag</label><select id="alias-tag"></select></div>
      <div class="field"><label for="alias-name">Alias</label><input id="alias-name" type="text" placeholder="PKM"></div>
      <p class="form-message" id="tag-alias-message" data-form-message hidden></p>
      <button type="submit">Add alias</button>
    </form>
    <section class="panel">
      <h3>Current tags</h3>
      <div id="tag-list" class="stack"></div>
    </section>
  </section>`;
}

async function bindTagSettingsPanel() {
  const tagForm = document.querySelector("#tag-form");
  const aliasForm = document.querySelector("#tag-alias-form");
  if (!tagForm || !aliasForm) return;
  await refreshTagSettings();
  tagForm.addEventListener("submit", async (event) => {
    event.preventDefault();
    const done = setButtonBusy(event.submitter, "Creating");
    setFormMessage(tagForm);
    try {
      await api("/tags", { method: "POST", body: JSON.stringify({ name: document.querySelector("#tag-name").value }) });
      document.querySelector("#tag-name").value = "";
      await refreshTagSettings();
      ui.toast("Tag saved", "success");
    } catch (err) {
      setFormMessage(tagForm, err.message);
      ui.toast(err.message, "error");
    } finally {
      done();
    }
  });
  aliasForm.addEventListener("submit", async (event) => {
    event.preventDefault();
    const done = setButtonBusy(event.submitter, "Adding alias");
    setFormMessage(aliasForm);
    try {
      await api("/tags/aliases", {
        method: "POST",
        body: JSON.stringify({
          tag_id: document.querySelector("#alias-tag").value,
          alias: document.querySelector("#alias-name").value,
        }),
      });
      document.querySelector("#alias-name").value = "";
      ui.toast("Alias saved", "success");
    } catch (err) {
      setFormMessage(aliasForm, err.message);
      ui.toast(err.message, "error");
    } finally {
      done();
    }
  });
}

async function refreshTagSettings() {
  const result = await api("/tags").catch(() => ({ tags: [] }));
  const tags = result.tags || [];
  const list = document.querySelector("#tag-list");
  const select = document.querySelector("#alias-tag");
  if (list) {
    list.innerHTML = tags.map((tag) => `<article class="annotation">
      <p><strong>${escapeHTML(tag.name)}</strong> <span class="meta">${escapeHTML(tag.slug)} · ${Number(tag.bookmark_count || 0)} saves</span></p>
    </article>`).join("") || `<p class="meta">No tags yet.</p>`;
  }
  if (select) {
    select.innerHTML = tags.map((tag) => `<option value="${escapeHTML(tag.id)}">${escapeHTML(tag.name)}</option>`).join("");
  }
}

function connectionsPanel() {
  return `<section class="split">
    <section class="panel">
      <h3>X</h3>
      <div id="x-status" class="stack"><p class="meta">Loading X status.</p></div>
      <div class="button-row">
        <button type="button" class="secondary" id="x-connect">Connect X</button>
        <button type="button" class="secondary" id="x-sync">Sync bookmarks</button>
        <button type="button" class="danger" id="x-disconnect">Disconnect</button>
      </div>
    </section>
  </section>`;
}

async function bindConnectionsPanel() {
  const status = document.querySelector("#x-status");
  if (!status) return;
  const connect = document.querySelector("#x-connect");
  const sync = document.querySelector("#x-sync");
  const disconnect = document.querySelector("#x-disconnect");
  let callbackError = "";
  const refresh = async () => {
    try {
      const enabled = await api("/auth/x/enabled");
      if (!enabled.enabled) {
        status.innerHTML = `<p class="meta">X is disabled. Ask an admin to configure X client keys and enable X integration.</p>`;
        connect.disabled = true;
        sync.disabled = true;
        disconnect.disabled = true;
        return;
      }
      const current = await api("/auth/x/status");
      status.innerHTML = xConnectionStatus(current);
      connect.hidden = current.connected;
      sync.disabled = !current.connected;
      disconnect.disabled = !current.connected;
    } catch (err) {
      status.innerHTML = `<p class="meta">${escapeHTML(err.message)}</p>`;
    }
  };
  const params = new URLSearchParams(location.search);
  const code = params.get("code");
  const oauthState = params.get("state");
  if (code && oauthState) {
    status.innerHTML = `<p class="meta">Completing X connection.</p>`;
    try {
      const current = await api("/auth/x/callback", {
        method: "POST",
        body: JSON.stringify({ code, state: oauthState }),
      });
      ui.toast(`Connected X${current.x_username ? ` as @${current.x_username}` : ""}`, "success");
    } catch (err) {
      callbackError = err.message || "X authorization failed.";
      ui.toast(callbackError, "error");
    } finally {
      const clean = new URL(location.href);
      clean.search = "?section=connections";
      history.replaceState({}, "", clean);
    }
  }
  connect.addEventListener("click", async (event) => {
    const done = setButtonBusy(event.currentTarget, "Opening");
    try {
      const result = await api("/auth/x/connect");
      if (result.auth_url) window.location.href = result.auth_url;
    } catch (err) {
      ui.toast(err.message, "error");
    } finally {
      done();
    }
  });
  sync.addEventListener("click", async (event) => {
    const done = setButtonBusy(event.currentTarget, "Syncing");
    try {
      const result = await api("/auth/x/sync", { method: "POST", body: "{}" });
      const created = Number(result.new_bookmarks || 0);
      const repaired = Number(result.repaired_bookmarks || 0);
      ui.toast(`Synced ${created} new and repaired ${repaired} bookmarks`, "success");
      await refresh();
    } catch (err) {
      ui.toast(err.message, "error");
    } finally {
      done();
    }
  });
  disconnect.addEventListener("click", async (event) => {
    const confirmed = await ui.confirmDestructive({ title: "Disconnect X", body: "This removes Arivu's stored X tokens. Saved bookmarks stay in Arivu.", confirm: "Disconnect", cancel: "Keep connected" });
    if (!confirmed) return;
    const done = setButtonBusy(event.currentTarget, "Disconnecting");
    try {
      await api("/auth/x/disconnect", { method: "POST", body: "{}" });
      ui.toast("X disconnected", "success");
      await refresh();
    } catch (err) {
      ui.toast(err.message, "error");
    } finally {
      done();
    }
  });
  await refresh();
  if (callbackError) {
    status.insertAdjacentHTML("afterbegin", `<p class="meta">${escapeHTML(callbackError)} Authorize X again. If this repeats, check the callback URL in Settings.</p>`);
  }
}

function xConnectionStatus(status) {
  if (!status.connected) return `<p class="meta">No X account connected.</p>`;
  return `<p><strong>@${escapeHTML(status.x_username || "x")}</strong> <span class="meta">${escapeHTML(status.x_name || "")}</span></p>
    <p class="meta">Status: ${escapeHTML(status.sync_status || "idle")} · Synced: ${Number(status.total_synced || 0)}${status.last_sync_at ? ` · Last sync: ${escapeHTML(status.last_sync_at)}` : ""}</p>`;
}

function modelProviderPreset(id) {
  return modelProviderPresets.find((provider) => provider.id === id) || modelProviderPresets.find((provider) => provider.id === "custom");
}

function modelProviderOptions(selected = "gemini") {
  return modelProviderPresets.map((provider) => `<option value="${escapeHTML(provider.id)}"${provider.id === selected ? " selected" : ""}>${escapeHTML(provider.label)}</option>`).join("");
}

function updateModelProviderHints(form, prefix) {
  const providerField = form.querySelector(`#${prefix}ai-provider`);
  const modelField = form.querySelector(`#${prefix}ai-model`);
  const baseURLField = form.querySelector(`#${prefix}ai-base-url`);
  if (!providerField || !modelField || !baseURLField) return;
  const preset = modelProviderPreset(providerField.value);
  modelField.placeholder = preset.defaultModel || "provider-model-id";
  baseURLField.placeholder = preset.baseURL || "https://api.example.com/v1";
}

function setModelProviderFields(form, prefix, settings) {
  const providerField = form.querySelector(`#${prefix}ai-provider`);
  const modelField = form.querySelector(`#${prefix}ai-model`);
  const apiKeyField = form.querySelector(`#${prefix}ai-api-key`);
  const baseURLField = form.querySelector(`#${prefix}ai-base-url`);
  if (!providerField || !modelField || !apiKeyField || !baseURLField) return;
  const providerID = settings.ai_provider?.value || providerField.value || "gemini";
  const preset = modelProviderPreset(providerID);
  providerField.value = preset.id;
  modelField.value = settings.ai_model?.value || "";
  apiKeyField.placeholder = "Leave blank to keep current value";
  baseURLField.value = settings.ai_base_url?.value || preset.baseURL || "";
  updateModelProviderHints(form, prefix);
}

function bindModelProviderDefaults(form, prefix) {
  const providerField = form.querySelector(`#${prefix}ai-provider`);
  const modelField = form.querySelector(`#${prefix}ai-model`);
  const apiKeyField = form.querySelector(`#${prefix}ai-api-key`);
  const baseURLField = form.querySelector(`#${prefix}ai-base-url`);
  if (!providerField || !modelField || !apiKeyField || !baseURLField) return;
  providerField.addEventListener("change", () => {
    const preset = modelProviderPreset(providerField.value);
    modelField.value = preset.defaultModel || "";
    apiKeyField.placeholder = "Enter a new key, or leave blank for keyless local use";
    baseURLField.value = preset.baseURL || "";
    updateModelProviderHints(form, prefix);
  });
  updateModelProviderHints(form, prefix);
}

function apiKeysPanel() {
  return `<section class="split">
    <section class="panel">
      <h3>Status</h3>
      <div id="api-key-status" class="stack"><p class="meta">Loading key status.</p></div>
    </section>
    <form class="panel form" id="api-keys-form">
      <h3>Update keys</h3>
      <div class="field"><label for="ai-provider">Model Provider</label><select id="ai-provider">${modelProviderOptions()}</select></div>
      <div class="field"><label for="ai-model">Model</label><input id="ai-model" type="text" autocomplete="off"></div>
      <div class="field"><label for="ai-api-key">API Key</label><input id="ai-api-key" type="password" autocomplete="off" placeholder="Leave blank to keep current value"></div>
      <div class="field"><label for="ai-base-url">Base URL</label><input id="ai-base-url" type="url" autocomplete="off"></div>
      <div class="field"><label for="resend-api-key">Resend API key</label><input id="resend-api-key" type="password" autocomplete="off" placeholder="Leave blank to keep current value"></div>
      <div class="field"><label for="resend-from-email">Resend from email</label><input id="resend-from-email" type="email" autocomplete="off"></div>
      <div class="field"><label for="x-client-id">X client ID</label><input id="x-client-id" type="text" autocomplete="off"></div>
      <div class="field"><label for="x-client-secret">X client secret</label><input id="x-client-secret" type="password" autocomplete="off" placeholder="Leave blank to keep current value"></div>
      <div class="field"><label for="x-redirect-uri">X redirect URI</label><input id="x-redirect-uri" type="url" autocomplete="off"></div>
      <label class="checkbox-row"><input id="x-integration-enabled" type="checkbox"> X integration enabled</label>
      <p class="form-message" id="api-keys-message" data-form-message hidden></p>
      <button type="submit">Save provider settings</button>
    </form>
  </section>`;
}

async function bindAPIKeysPanel() {
  const form = document.querySelector("#api-keys-form");
  const status = document.querySelector("#api-key-status");
  if (!form || !status) return;
  bindModelProviderDefaults(form, "");
  const refresh = async () => {
    try {
      const keys = await api("/admin/api-keys");
      status.innerHTML = apiKeyStatus(keys);
      status.querySelectorAll("[data-api-key-revert]").forEach((button) => {
        button.addEventListener("click", async () => {
          const done = setButtonBusy(button, "Reverting");
          try {
            await api(`/admin/api-keys/${button.dataset.apiKeyRevert}`, { method: "DELETE" });
            await refresh();
            ui.toast("Override removed", "success");
          } catch (err) {
            ui.toast(err.message, "error");
          } finally {
            done();
          }
        });
      });
      document.querySelector("#x-redirect-uri").value = keys.x_redirect_uri?.value || "";
      document.querySelector("#x-integration-enabled").checked = Boolean(keys.x_integration_enabled?.value);
      setModelProviderFields(form, "", keys);
      document.querySelector("#resend-from-email").value = keys.resend_from_email?.value || "";
    } catch (err) {
      status.innerHTML = `<p class="meta">${escapeHTML(err.status === 403 ? "Admin access required." : err.message)}</p>`;
    }
  };
  await refresh();
  form.addEventListener("submit", async (event) => {
    event.preventDefault();
    const done = setButtonBusy(event.submitter, "Saving");
    setFormMessage(form);
    const body = {
      ai_provider: document.querySelector("#ai-provider").value,
    };
    for (const [key, selector] of Object.entries({
      ai_api_key: "#ai-api-key",
      ai_model: "#ai-model",
      ai_base_url: "#ai-base-url",
      resend_api_key: "#resend-api-key",
      x_client_id: "#x-client-id",
      x_client_secret: "#x-client-secret",
      resend_from_email: "#resend-from-email",
      x_redirect_uri: "#x-redirect-uri",
    })) {
      const value = document.querySelector(selector).value.trim();
      if (value) body[key] = value;
    }
    body.x_integration_enabled = document.querySelector("#x-integration-enabled").checked;
    try {
      await api("/admin/api-keys", { method: "PUT", body: JSON.stringify(body) });
      form.reset();
      await refresh();
      ui.toast("API keys saved", "success");
    } catch (err) {
      setFormMessage(form, err.message);
      ui.toast(err.message, "error");
    } finally {
      done();
    }
  });
}

function apiKeyStatus(keys) {
  const labels = {
    ai_provider: "Model Provider",
    ai_model: "Model",
    ai_api_key: "API Key",
    ai_base_url: "Base URL",
    resend_api_key: "Resend API key",
    resend_from_email: "Resend from email",
    x_client_id: "X client ID",
    x_client_secret: "X client secret",
    x_redirect_uri: "X redirect URI",
    x_integration_enabled: "X enabled",
  };
  return settingsStatusRows(keys, labels, "api-key-revert");
}

function settingsStatusRows(settings, labels, revertAttribute) {
  return Object.keys(labels).map((key) => {
    const item = settings[key] || {};
    const value = item.masked_value || (item.value === undefined || item.value === null ? "" : String(item.value));
    const configured = item.configured || value;
    const revertKey = settingRevertKey(key, item);
    return `<article class="annotation">
      <p><strong>${escapeHTML(labels[key])}</strong> <span class="meta">${configured ? "configured" : "not configured"} · ${escapeHTML(item.source || "unset")}${value ? ` · ${escapeHTML(value)}` : ""}</span></p>
      ${revertKey ? `<p class="button-row"><button type="button" class="secondary" data-${revertAttribute}="${escapeHTML(revertKey)}">Remove override</button></p>` : ""}
    </article>`;
  }).join("");
}

function settingRevertKey(key, item) {
  if (item.source === "database") return key;
  if (item.source !== "legacy_database") return "";
  return {
    ai_api_key: "gemini_api_key",
    ai_model: "gemini_model",
    ai_base_url: "gemini_base_url",
  }[key] || "";
}

function adminSettingsPanel(settings) {
  const selectedProvider = settings.ai_provider?.value || "gemini";
  return `<section class="split">
    <section class="panel">
      <h3>Runtime status</h3>
      <div id="admin-settings-status" class="stack">${settingsStatus(settings)}</div>
    </section>
    <form class="panel form" id="admin-settings-form">
      <h3>Runtime settings</h3>
      <div class="field"><label for="admin-app-url">Public app URL</label><input id="admin-app-url" type="url" autocomplete="url" value="${escapeHTML(settings.app_url?.value || "")}"></div>
      <label class="checkbox-row"><input id="admin-signups-enabled" type="checkbox" ${settings.signups_enabled?.value ? "checked" : ""}> Public signups enabled</label>
      <label class="checkbox-row"><input id="admin-cookie-secure" type="checkbox" ${settings.cookie_secure?.value ? "checked" : ""}> Secure browser cookies</label>
      <hr>
      <div class="field"><label for="admin-ai-provider">Model Provider</label><select id="admin-ai-provider">${modelProviderOptions(selectedProvider)}</select></div>
      <div class="field"><label for="admin-ai-model">Model</label><input id="admin-ai-model" type="text" autocomplete="off" value="${escapeHTML(settings.ai_model?.value || "")}"></div>
      <div class="field"><label for="admin-ai-api-key">API Key</label><input id="admin-ai-api-key" type="password" autocomplete="off" placeholder="Leave blank to keep current value"></div>
      <div class="field"><label for="admin-ai-base-url">Base URL</label><input id="admin-ai-base-url" type="url" autocomplete="off" value="${escapeHTML(settings.ai_base_url?.value || "")}"></div>
      <div class="field"><label for="admin-resend-api-key">Resend API key</label><input id="admin-resend-api-key" type="password" autocomplete="off" placeholder="Leave blank to keep current value"></div>
      <div class="field"><label for="admin-resend-from-email">Resend from email</label><input id="admin-resend-from-email" type="email" autocomplete="off" value="${escapeHTML(settings.resend_from_email?.value || "")}"></div>
      <div class="field"><label for="admin-x-client-id">X client ID</label><input id="admin-x-client-id" type="text" autocomplete="off"></div>
      <div class="field"><label for="admin-x-client-secret">X client secret</label><input id="admin-x-client-secret" type="password" autocomplete="off" placeholder="Leave blank to keep current value"></div>
      <div class="field"><label for="admin-x-redirect-uri">X redirect URI</label><input id="admin-x-redirect-uri" type="url" autocomplete="off" value="${escapeHTML(settings.x_redirect_uri?.value || "")}"></div>
      <label class="checkbox-row"><input id="admin-x-integration-enabled" type="checkbox" ${settings.x_integration_enabled?.value ? "checked" : ""}> X integration enabled</label>
      <p class="form-message" id="admin-settings-message" data-form-message hidden></p>
      <button type="submit">Save settings</button>
    </form>
  </section>`;
}

function settingsStatus(settings) {
  const labels = {
    app_url: "Public app URL",
    signups_enabled: "Public signups",
    cookie_secure: "Secure cookies",
    ai_provider: "Model Provider",
    ai_model: "Model",
    ai_api_key: "API Key",
    ai_base_url: "Base URL",
    resend_api_key: "Resend API key",
    resend_from_email: "Resend from email",
    x_client_id: "X client ID",
    x_client_secret: "X client secret",
    x_redirect_uri: "X redirect URI",
    x_integration_enabled: "X enabled",
  };
  return settingsStatusRows(settings, labels, "admin-setting-revert");
}

function bindAdminSettingsPanel() {
  const form = document.querySelector("#admin-settings-form");
  const status = document.querySelector("#admin-settings-status");
  if (!form || !status) return;
  let mutationBusy = false;
  const field = (selector) => form.querySelector(selector);
  bindModelProviderDefaults(form, "admin-");
  const refresh = async () => {
    const settings = await api("/admin/settings");
    if (!form.isConnected || !status.isConnected) return;
    status.innerHTML = settingsStatus(settings);
    field("#admin-app-url").value = settings.app_url?.value || "";
    field("#admin-signups-enabled").checked = Boolean(settings.signups_enabled?.value);
    field("#admin-cookie-secure").checked = Boolean(settings.cookie_secure?.value);
    setModelProviderFields(form, "admin-", settings);
    field("#admin-resend-from-email").value = settings.resend_from_email?.value || "";
    field("#admin-x-redirect-uri").value = settings.x_redirect_uri?.value || "";
    field("#admin-x-integration-enabled").checked = Boolean(settings.x_integration_enabled?.value);
    status.querySelectorAll("[data-admin-setting-revert]").forEach((button) => {
      button.addEventListener("click", async () => {
        if (mutationBusy) return;
        mutationBusy = true;
        const done = setButtonBusy(button, "Reverting");
        try {
          await api(`/admin/settings/${button.dataset.adminSettingRevert}`, { method: "DELETE" });
          await refresh();
          ui.toast("Override removed", "success");
        } catch (err) {
          ui.toast(err.message, "error");
        } finally {
          done();
          mutationBusy = false;
        }
      });
    });
  };
  form.addEventListener("submit", async (event) => {
    event.preventDefault();
    if (mutationBusy) return;
    mutationBusy = true;
    const done = setButtonBusy(event.submitter, "Saving");
    setFormMessage(form);
    const body = {
      app_url: field("#admin-app-url").value.trim(),
      signups_enabled: field("#admin-signups-enabled").checked,
      cookie_secure: field("#admin-cookie-secure").checked,
      x_integration_enabled: field("#admin-x-integration-enabled").checked,
      ai_provider: field("#admin-ai-provider").value,
    };
    for (const [key, selector] of Object.entries({
      ai_api_key: "#admin-ai-api-key",
      ai_model: "#admin-ai-model",
      ai_base_url: "#admin-ai-base-url",
      resend_api_key: "#admin-resend-api-key",
      x_client_id: "#admin-x-client-id",
      x_client_secret: "#admin-x-client-secret",
      resend_from_email: "#admin-resend-from-email",
      x_redirect_uri: "#admin-x-redirect-uri",
    })) {
      const value = field(selector).value.trim();
      if (value) body[key] = value;
    }
    try {
      await api("/admin/settings", { method: "PUT", body: JSON.stringify(body) });
      form.querySelectorAll("input[type=password]").forEach((input) => { input.value = ""; });
      await refresh();
      ui.toast("Settings saved", "success");
    } catch (err) {
      setFormMessage(form, err.message);
      ui.toast(err.message, "error");
    } finally {
      done();
      mutationBusy = false;
    }
  });
}

function importPanel() {
  return `<section class="split">
    <form class="panel form" id="import-form">
      <h3>Import or restore</h3>
      <p class="meta">Paste supported exports or one URL per line. Imports are queued safely; full JSON restores rebuild your second-brain data under this account.</p>
      <div class="field"><label for="import-content">Import or restore content</label><textarea id="import-content" rows="9" placeholder="Paste browser, Pocket, Raindrop, Linkwarden, OPML, RSS/Atom, URL-bearing Readwise/Kindle CSV, Arivu JSON, or one URL per line"></textarea></div>
      <p class="form-message" id="import-message" data-form-message hidden></p>
      <button type="submit">Queue import or restore</button>
    </form>
    <form class="panel form" id="media-import-form">
      <h3>Document and transcript import</h3>
      <p class="meta">Create a searchable note from a PDF, EPUB, image OCR text, plain text file, or YouTube/video transcript.</p>
      <div class="field"><label for="media-import-title">Title</label><input id="media-import-title" name="title" placeholder="Research packet, book, talk, screenshot"></div>
      <div class="field"><label for="media-import-url">Source URL</label><input id="media-import-url" name="source_url" placeholder="https://youtube.com/watch?v=..."></div>
      <div class="field"><label for="media-import-file">File</label><input id="media-import-file" name="file" type="file" accept=".epub,.pdf,.txt,.md,.html,.htm,image/*"></div>
      <div class="field"><label for="media-import-transcript">Transcript or OCR text</label><textarea id="media-import-transcript" name="transcript" rows="7" placeholder="Paste video transcript, OCR text, or copied document text"></textarea></div>
      <p class="form-message" id="media-import-message" data-form-message hidden></p>
      <button type="submit">Import as note</button>
    </form>
    <form class="panel form" id="calendar-import-form">
      <h3>Calendar import</h3>
      <p class="meta">Paste an ICS export to create meeting objects with start, end, location, description, and UID fields.</p>
      <div class="field"><label for="calendar-import-source">Source</label><input id="calendar-import-source" type="text" placeholder="calendar.ics"></div>
      <div class="field"><label for="calendar-import-ics">ICS content</label><textarea id="calendar-import-ics" rows="8" spellcheck="false" placeholder="BEGIN:VCALENDAR&#10;BEGIN:VEVENT&#10;SUMMARY:Research review&#10;END:VEVENT&#10;END:VCALENDAR"></textarea></div>
      <p class="form-message" id="calendar-import-message" data-form-message hidden></p>
      <button type="submit">Import meetings</button>
    </form>
    <section class="panel">
      <h3>Export</h3>
      <div class="button-row">
        <a class="button secondary" href="/api/bookmarks/export?format=json">Full JSON</a>
        <a class="button secondary" href="/api/bookmarks/export?format=csv">CSV</a>
        <a class="button secondary" href="/api/bookmarks/export?format=html">Browser HTML</a>
        <a class="button secondary" href="/api/bookmarks/export?format=markdown">Markdown</a>
        <a class="button secondary" href="/api/bookmarks/export?format=obsidian">Obsidian ZIP</a>
      </div>
      <div id="import-jobs" class="stack"></div>
    </section>
  </section>`;
}

async function bindImportPanel() {
  const form = document.querySelector("#import-form");
  if (!form) return;
  const jobs = await api("/import-jobs").catch(() => []);
  renderImportJobs(jobs);
  form.addEventListener("submit", async (event) => {
    event.preventDefault();
    const done = setButtonBusy(event.submitter, "Importing");
    setFormMessage(form);
    try {
      const result = await api("/bookmarks/import", {
        method: "POST",
        headers: { "Content-Type": "text/plain" },
        body: document.querySelector("#import-content").value,
      });
      setFormMessage(form, `${result.count || 0} bookmarks queued.`, "success");
      ui.toast(`${result.count || 0} bookmarks queued`, "success");
      const latest = await api("/import-jobs").catch(() => []);
      if (result.import_job_id) {
        const detail = await api(`/import-jobs/${result.import_job_id}`).catch(() => null);
        if (detail) {
          const index = latest.findIndex((job) => job.id === result.import_job_id);
          if (index >= 0) latest[index] = detail;
        }
      }
      renderImportJobs(latest);
    } catch (err) {
      setFormMessage(form, err.message);
      ui.toast(err.message, "error");
    } finally {
      done();
    }
  });
  bindMediaImportPanel();
  bindCalendarImportPanel();
}

function bindMediaImportPanel() {
  const form = document.querySelector("#media-import-form");
  if (!form) return;
  form.addEventListener("submit", async (event) => {
    event.preventDefault();
    const done = setButtonBusy(event.submitter, "Importing");
    setFormMessage(form);
    try {
      const formData = new FormData(form);
      const result = await api("/media/import", { method: "POST", body: formData });
      const title = result.note?.title || "Imported media";
      setFormMessage(form, `Saved "${title}" as a searchable note.`, "success");
      ui.toast("Media imported as a note", "success");
      form.reset();
    } catch (err) {
      setFormMessage(form, err.message);
      ui.toast(err.message, "error");
    } finally {
      done();
    }
  });
}

function bindCalendarImportPanel() {
  const form = document.querySelector("#calendar-import-form");
  if (!form) return;
  form.addEventListener("submit", async (event) => {
    event.preventDefault();
    const done = setButtonBusy(event.submitter, "Importing");
    setFormMessage(form);
    try {
      const result = await api("/calendar/import", {
        method: "POST",
        body: JSON.stringify({
          source: document.querySelector("#calendar-import-source").value,
          ics: document.querySelector("#calendar-import-ics").value,
        }),
      });
      setFormMessage(form, `${Number(result.count || 0)} meeting objects imported.`, "success");
      ui.toast(`${Number(result.count || 0)} meetings imported`, "success");
      form.reset();
    } catch (err) {
      setFormMessage(form, err.message);
      ui.toast(err.message, "error");
    } finally {
      done();
    }
  });
}

function renderImportJobs(jobs) {
  const target = document.querySelector("#import-jobs");
  if (!target) return;
  target.innerHTML = (jobs || []).map((job) => `<article class="annotation">
    <p><strong>${escapeHTML(importJobLabel(job))}</strong> <span class="meta">${Number(job.total_bookmarks || 0)} items</span></p>
    ${importJobProgress(job)}
    ${importSourceReport(job.source_report || [])}
    ${importSourceItems(job.items || [])}
    <p class="meta">Fetched ${Number(job.content_fetched || 0)} · AI ${Number(job.ai_processed || 0)} · Failed ${Number(job.failed || 0)}</p>
  </article>`).join("") || `<p class="meta">No imports yet.</p>`;
}

function importJobLabel(job) {
  const failed = Number(job.failed || 0);
  if (failed) return `${job.status || "import"} · ${failed} failed`;
  return job.status || "import";
}

function importJobProgress(job) {
  const total = Number(job.total_bookmarks || 0);
  if (!total) return "";
  const handled = Math.min(total, Number(job.content_fetched || 0) + Number(job.failed || 0));
  return `<progress value="${handled}" max="${total}" aria-label="Import progress"></progress><p class="meta">${handled}/${total} handled${job.updated_at ? ` · updated ${escapeHTML(formatDate(job.updated_at))}` : ""}</p>`;
}

function importSourceReport(report) {
  if (!report.length) return "";
  return `<div class="chips">${report.map((item) => `<span>${escapeHTML(item.source || "import")} · ${Number(item.count || 0)}</span>`).join("")}</div>`;
}

function importSourceItems(items) {
  if (!items.length) return "";
  return `<div class="stack">${items.slice(0, 5).map((item) => `<p class="meta">${escapeHTML(item.source || "import")} · ${escapeHTML(item.title || item.url || "Imported item")}</p>`).join("")}</div>`;
}

async function reviewPage() {
  await requireUser();
  const [queue, memory] = await Promise.all([
    api("/review?limit=12"),
    api("/memory-jogger").catch(() => ({ has_memory: false })),
  ]);
  const memoryID = memory.has_memory && memory.bookmark ? memory.bookmark.id : "";
  const reviewItems = (queue.items || []).filter((item) => item.item_type === "note" || item.id !== memoryID);
  const reviewEmpty = memoryID
    ? { eyebrow: "Caught up", title: "No additional review items due", body: "Finish the daily memory above and your review queue is clear." }
    : { eyebrow: "Clear", title: "No review items due", body: "Arivu will bring older or high-signal saves back when they are ready." };
  setRoot(shell("Review", `<div class="home-view review-view">
    ${homeViewTabs("review")}
    <section class="review-overview">
      ${memoryCard(memory)}
      <section class="panel">
        <span class="meta">Daily review</span>
        <h2>Keep saved pages from becoming a pile</h2>
        <p>Complete what is useful, snooze what needs time, archive what should stop coming back for review.</p>
      </section>
    </section>
    <section class="review-grid" aria-label="Review queue">
      ${reviewItems.map(reviewCard).join("") || emptyState(reviewEmpty)}
    </section>
  </div>`));
  document.querySelectorAll("[data-review-complete]").forEach((button) => {
    button.addEventListener("click", () => reviewAction(button, "complete"));
  });
  document.querySelectorAll("[data-review-snooze]").forEach((button) => {
    button.addEventListener("click", () => reviewAction(button, "snooze"));
  });
  document.querySelectorAll("[data-review-archive]").forEach((button) => {
    button.addEventListener("click", () => reviewAction(button, "archive"));
  });
  bindFeedbackControls();
  bindActionItemControls();
  bindReminderControls();
}

function memoryCard(memory) {
  if (!memory.has_memory || !memory.bookmark) {
    return emptyState({ eyebrow: "Daily memory", title: "No memory due", body: memory.message || "Older, high-signal saves will appear here.", tag: "section" });
  }
  const item = memory.bookmark;
  const context = memory.context || {};
  const id = `bookmark:${item.id}`;
  return `<section class="panel">
    <span class="meta">Daily memory · ${escapeHTML(context.reason || item.domain || "review")}</span>
    <h2>${escapeHTML(item.title || item.url || "Untitled")}</h2>
    <p>${escapeHTML(item.ai_summary?.one_sentence || item.description || "")}</p>
    <p class="meta">${Number(context.days_since_accessed || 0)} days since last review</p>
    <p class="button-row">
      <a class="button secondary" href="/bookmark/${escapeHTML(item.id)}">Open</a>
      <button type="button" data-review-complete="${escapeHTML(id)}">Complete review</button>
      <button type="button" class="secondary" data-review-snooze="${escapeHTML(id)}">Snooze</button>
      <button type="button" class="secondary" data-review-archive="${escapeHTML(id)}">Archive</button>
    </p>
  </section>`;
}

function reviewCard(item) {
  const id = `${item.item_type || "bookmark"}:${item.id}`;
  const isNote = item.item_type === "note";
  const itemState = item.item_state || {};
  const nextAction = itemState.next_action || "";
  const importance = Number(itemState.importance || 0);
  const reasons = item.review_reasons || [];
  return `<article class="panel bookmark">
    <span class="meta">Why this came back: ${escapeHTML(item.resurfacing_reason || item.domain || item.source || "review")} · priority ${Number(item.review_priority || 0)}</span>
    <h2>${escapeHTML(item.title || item.url || "Untitled")}</h2>
    <p class="review-summary">${escapeHTML(item.description || item.ai_summary?.one_sentence || "")}</p>
    ${reasons.length ? `<div class="chips">${reasons.slice(0, 4).map((reason) => `<span>${escapeHTML(reason)}</span>`).join("")}</div>` : ""}
    ${feedbackControls(item.item_type || "bookmark", item.id || "", "review", item.feedback_state)}
    ${nextAction || importance ? `<p class="meta">${nextAction ? `Next: ${escapeHTML(nextAction)}` : ""}${nextAction && importance ? " · " : ""}${importance ? `Priority ${importance}` : ""}</p>` : ""}
    <p class="button-row">
      <a class="button secondary" href="${isNote ? `/notes/${encodeURIComponent(item.id)}` : `/bookmark/${escapeHTML(item.id)}`}">Open</a>
      <button type="button" data-review-complete="${escapeHTML(id)}">Complete review</button>
      <button type="button" class="secondary" data-review-snooze="${escapeHTML(id)}">Snooze</button>
      ${isNote ? "" : `<button type="button" class="secondary" data-review-archive="${escapeHTML(id)}">Archive</button>`}
    </p>
    <details class="review-followup">
      <summary>Add task or reminder</summary>
      <div class="review-followup-body">
        <section>
          <h3>Task</h3>
          ${actionItemsPanel(item.item_type || "bookmark", item.id, item.action_items || [])}
        </section>
        <section>
          <h3>Reminder</h3>
          ${reminderForm(item.item_type || "bookmark", item.id)}
          ${reminderList(item.reminders || [])}
        </section>
      </div>
    </details>
  </article>`;
}

function feedbackControls(itemType, itemID, surface, stateValue = "") {
  if (!itemID) return "";
  const options = [
    ["useful", "Useful"],
    ["not_useful", "Not useful"],
    ["snooze_longer", "Snooze longer"],
    ["never_resurface", "Never resurface"],
  ];
  return `<div class="chips feedback-controls" aria-label="Feedback">${options.map(([value, label]) => `<button type="button" class="secondary ${stateValue === value ? "active" : ""}" data-feedback="${value}" data-feedback-type="${escapeHTML(itemType)}" data-feedback-id="${escapeHTML(itemID)}" data-feedback-surface="${escapeHTML(surface)}" aria-pressed="${stateValue === value}">${label}</button>`).join("")}</div>`;
}

function bindFeedbackControls() {
  document.querySelectorAll("[data-feedback]").forEach((button) => {
    button.addEventListener("click", async () => {
      const done = setButtonBusy(button, "Saving");
      try {
        await api("/feedback", {
          method: "POST",
          body: JSON.stringify({
            item_type: button.dataset.feedbackType,
            item_id: button.dataset.feedbackId,
            surface: button.dataset.feedbackSurface,
            feedback: button.dataset.feedback,
          }),
        });
        button.closest(".feedback-controls")?.querySelectorAll("[data-feedback]").forEach((item) => {
          const active = item === button;
          item.classList.toggle("active", active);
          item.setAttribute("aria-pressed", String(active));
        });
        ui.toast("Feedback saved", "success");
      } catch (err) {
        ui.toast(err.message, "error");
      } finally {
        done();
      }
    });
  });
}

async function reviewAction(button, action) {
  const item = button.dataset.reviewComplete || button.dataset.reviewSnooze;
  const archiveItem = button.dataset.reviewArchive;
  const target = item || archiveItem;
  const done = setButtonBusy(button, action === "complete" ? "Completing" : action === "archive" ? "Archiving" : "Snoozing");
  try {
    if (action === "archive") {
      const [, bookmarkID] = String(target).split(":");
      await api(`/resurfacing/${bookmarkID}/archive`, { method: "POST", body: "{}" });
    } else {
      await api(`/review/${target}/${action}`, { method: "POST", body: action === "snooze" ? JSON.stringify({ days: 7 }) : "{}" });
    }
    ui.toast(action === "complete" ? "Review completed" : action === "archive" ? "Archived from review" : "Review snoozed", "success");
    render();
  } catch (err) {
    ui.toast(err.message, "error");
  } finally {
    done();
  }
}

async function duplicatesPage() {
  await requireUser();
  const result = await api("/bookmarks/duplicates/detect");
  const groups = result.duplicates || [];
  setRoot(shell("Duplicates", `
    <section class="panel">
      <span class="meta">Library hygiene</span>
      <h2>${groups.length} duplicate groups</h2>
      <p>Merge repeated saves without losing collection links, summaries, or reading history.</p>
    </section>
    <section class="stack">
      ${groups.map(duplicateGroup).join("") || emptyState({ eyebrow: "Clean", title: "No duplicates found", body: "Exact URL and high-similarity matches will appear here." })}
    </section>
  `));
  document.querySelectorAll("[data-merge]").forEach((button) => {
    button.addEventListener("click", async () => {
      const ids = button.dataset.merge.split(",");
      const confirmed = await ui.confirmDestructive({ title: "Merge duplicates", body: "Merge this group into the top bookmark shown. Arivu keeps its URL and moves summaries, links, tags, notes, and reading history from the duplicates.", confirm: "Merge into top bookmark", cancel: "Keep separate" });
      if (!confirmed) return;
      const done = setButtonBusy(button, "Merging");
      try {
        await api("/bookmarks/merge", { method: "POST", body: JSON.stringify(ids) });
        ui.toast("Bookmarks merged", "success");
        render();
      } catch (err) {
        ui.toast(err.message, "error");
      } finally {
        done();
      }
    });
  });
}

function duplicateGroup(group) {
  const items = group.bookmarks || [];
  const ids = items.map((item) => item.id).join(",");
  return `<article class="panel">
    <span class="meta">${escapeHTML(group.type || "duplicate")} · ${items.length} saves</span>
    <div class="stack">${items.map((item) => `<a class="text-link" href="/bookmark/${escapeHTML(item.id)}">${escapeHTML(item.title || item.url)}</a>`).join("")}</div>
    ${items.length > 1 ? `<p class="button-row"><button type="button" data-merge="${escapeHTML(ids)}">Merge group</button></p>` : ""}
  </article>`;
}

async function graphPage() {
  await requireUser();
  const params = new URLSearchParams(location.search);
  const focus = params.get("focus") || "";
  const graph = await api(`/knowledge-graph/v2?node_limit=48&edge_limit=160&depth=1${focus ? `&focus=${encodeURIComponent(focus)}` : ""}`);
  const nodes = (graph.nodes || []).slice(0, 48);
  const edges = (graph.edges || []).filter((edge) => nodes.some((node) => node.id === edge.from) && nodes.some((node) => node.id === edge.to)).slice(0, 160);
  const positions = graphPositions(nodes);
  setRoot(shell("Graph", `
    <section class="graph-intro">
      <div><p class="lede">Explore how sources, notes, people, concepts, and highlights relate.</p><p class="meta">A focused, bounded view · ${nodes.length} nodes · ${edges.length} relationships${graph.truncated ? " · expand intentionally" : ""}</p></div>
      <form class="graph-focus" id="graph-focus-form">
        <label for="graph-focus">Focus node</label>
        <select id="graph-focus"><option value="">Recent knowledge</option>${nodes.map((node) => `<option value="${escapeHTML(node.id)}"${focus === node.id ? " selected" : ""}>${escapeHTML(node.title || node.id)}</option>`).join("")}</select>
        <button type="submit" class="secondary">Focus</button>
      </form>
    </section>
    <section class="graph-legend" aria-label="Graph legend">
      ${[...new Set(nodes.map((node) => node.type))].map((type) => `<span data-kind="${escapeHTML(type)}"><i aria-hidden="true"></i>${escapeHTML(knowledgeTypeLabel(type))}</span>`).join("")}
      <span><i class="edge-explicit" aria-hidden="true"></i>Explicit</span>
      <span><i class="edge-inferred" aria-hidden="true"></i>Derived</span>
    </section>
    <div class="graph-controls" role="group" aria-label="Graph view controls">
      <button type="button" class="secondary" data-graph-zoom="out">Zoom out</button>
      <button type="button" class="secondary" data-graph-zoom="reset">Reset view</button>
      <button type="button" class="secondary" data-graph-zoom="in">Zoom in</button>
    </div>
    <section class="graph-workspace">
      <div class="graph-canvas" role="region" aria-label="Interactive knowledge graph" tabindex="0">
        ${graphSVG(nodes, edges, positions)}
      </div>
      <aside class="graph-inspector" id="graph-inspector" aria-live="polite">${graphInspector(nodes[0], edges, nodes)}</aside>
    </section>
    <details class="graph-list" open>
      <summary>Accessible node list</summary>
      <p class="meta">This list contains the same nodes as the visual graph and supports full keyboard navigation.</p>
      <ul>${nodes.map((node) => `<li><a class="graph-list-node" href="${escapeHTML(knowledgeItemHref(node.type, node.source_id, node.title))}"><span>${escapeHTML(node.title || node.id)}</span><small>${escapeHTML(knowledgeTypeLabel(node.type))}</small></a></li>`).join("")}</ul>
    </details>
  `));
  document.querySelector("#graph-focus-form").addEventListener("submit", (event) => {
    event.preventDefault();
    const value = document.querySelector("#graph-focus").value;
    navigate(`/graph${value ? `?focus=${encodeURIComponent(value)}` : ""}`);
  });
  bindRelationshipFeedback();
  bindGraphViewport();
}

function bindGraphViewport() {
  const canvas = document.querySelector(".graph-canvas");
  const svg = canvas?.querySelector("svg");
  if (!canvas || !svg) return;
  let zoom = 1;
  const apply = () => {
    svg.style.width = `${zoom * 100}%`;
    svg.style.height = `${zoom * 100}%`;
    canvas.scrollTo({ left: (canvas.scrollWidth - canvas.clientWidth) / 2, top: (canvas.scrollHeight - canvas.clientHeight) / 2, behavior: "smooth" });
  };
  document.querySelectorAll("[data-graph-zoom]").forEach((button) => button.addEventListener("click", () => {
    const action = button.dataset.graphZoom;
    zoom = action === "reset" ? 1 : Math.min(2, Math.max(1, zoom + (action === "in" ? .25 : -.25)));
    apply();
  }));
}

function graphPositions(nodes) {
  const center = { x: 480, y: 300 };
  return Object.fromEntries(nodes.map((node, index) => {
    if (index === 0) return [node.id, center];
    const angle = index * 2.399963;
    const ring = Math.ceil(Math.sqrt(index));
    const radius = Math.min(250, 58 + ring * 34);
    return [node.id, { x: center.x + Math.cos(angle) * radius, y: center.y + Math.sin(angle) * radius }];
  }));
}

function graphSVG(nodes, edges, positions) {
  if (!nodes.length) return emptyState({ eyebrow: "No graph yet", title: "Capture something to begin", body: "Nodes and relationships appear here as your library grows.", panel: false });
  return `<svg viewBox="0 0 960 600" aria-labelledby="graph-svg-title graph-svg-description">
    <title id="graph-svg-title">Knowledge graph</title>
    <desc id="graph-svg-description">A bounded visual map of ${nodes.length} knowledge nodes and ${edges.length} relationships. Use Tab to inspect nodes or use the equivalent list below.</desc>
    <g class="graph-edges">${edges.map((edge) => {
      const from = positions[edge.from]; const to = positions[edge.to];
      if (!from || !to) return "";
      return `<line x1="${from.x}" y1="${from.y}" x2="${to.x}" y2="${to.y}" class="${edge.type === "explicit" ? "explicit" : "inferred"}" data-edge="${escapeHTML(edge.id)}"><title>${escapeHTML(knowledgeTypeLabel(edge.type))}, ${Math.round(Number(edge.confidence || 0) * 100)}% confidence</title></line>`;
    }).join("")}</g>
    <g class="graph-nodes">${nodes.map((node) => {
      const point = positions[node.id];
      return `<a href="${escapeHTML(knowledgeItemHref(node.type, node.source_id, node.title))}" data-graph-node="${escapeHTML(node.id)}" aria-label="${escapeHTML(`Open ${node.title || node.id}, ${knowledgeTypeLabel(node.type)}`)}"><g class="graph-node" data-kind="${escapeHTML(node.type)}" transform="translate(${point.x} ${point.y})"><circle r="${node === nodes[0] ? 12 : 8}"></circle><text y="-15">${escapeHTML(String(node.title || node.id).slice(0, 24))}</text></g></a>`;
    }).join("")}</g>
  </svg>`;
}

function graphInspector(node, edges, nodes) {
  if (!node) return emptyState({ eyebrow: "No selection", title: "Choose a node", body: "Select a node in the graph or accessible list.", panel: false });
  const relationships = edges.filter((edge) => edge.from === node.id || edge.to === node.id);
  return `<p class="meta">${escapeHTML(knowledgeTypeLabel(node.type))}</p>
    <h2>${escapeHTML(node.title || node.id)}</h2>
    <p>${escapeHTML(node.summary || "No summary yet.")}</p>
    <p><a class="text-link" href="${knowledgeItemHref(node.type, node.source_id, node.title)}">Open item</a></p>
    <h3>Relationships</h3>
    <div class="relationship-list">${relationships.map((edge) => {
      const otherID = edge.from === node.id ? edge.to : edge.from;
      const other = nodes.find((item) => item.id === otherID);
      const canConfirm = [edge.from, edge.to].every((id) => ["bookmark", "note"].includes(String(id).split(":")[0]));
      return `<article><p><strong>${escapeHTML(other?.title || otherID)}</strong></p><p class="meta">${escapeHTML(knowledgeTypeLabel(edge.type))} · ${Math.round(Number(edge.confidence || 0) * 100)}% confidence · ${escapeHTML(edge.provenance || "local")}</p>${edge.type !== "explicit" ? `<div class="relationship-actions">${canConfirm ? `<button class="secondary" type="button" data-relationship-feedback="confirm" data-edge-id="${escapeHTML(edge.id)}" data-from="${escapeHTML(edge.from)}" data-to="${escapeHTML(edge.to)}">Confirm</button>` : `<button class="secondary" type="button" data-relationship-feedback="useful" data-edge-id="${escapeHTML(edge.id)}" data-from="${escapeHTML(edge.from)}" data-to="${escapeHTML(edge.to)}">Useful</button>`}<button class="secondary" type="button" data-relationship-feedback="dismiss" data-edge-id="${escapeHTML(edge.id)}" data-from="${escapeHTML(edge.from)}" data-to="${escapeHTML(edge.to)}">Dismiss</button></div>` : ""}</article>`;
    }).join("") || `<p class="meta">No visible relationships in this focused view.</p>`}</div>`;
}

function bindRelationshipFeedback() {
  document.querySelectorAll("[data-relationship-feedback]").forEach((button) => button.addEventListener("click", async () => {
    const done = setButtonBusy(button, "Saving");
    try {
      await api("/feedback", { method: "POST", body: JSON.stringify({ target_type: "relationship", target_id: button.dataset.edgeId, feedback: button.dataset.relationshipFeedback, from: button.dataset.from || "", to: button.dataset.to || "" }) });
      ui.toast(button.dataset.relationshipFeedback === "confirm" ? "Connection confirmed" : "Suggestion dismissed", "success");
      if (button.dataset.relationshipFeedback === "dismiss") button.closest("article")?.remove();
    } catch (err) { ui.toast(err.message, "error"); } finally { done(); }
  }));
}

async function insightsPage() {
  await requireUser();
  const params = new URLSearchParams(location.search);
  if (params.get("legacy") === "evolution") return evolutionPage();
  const family = params.get("family") || "";
  const insightQuery = new URLSearchParams({ limit: "40" });
  if (family) insightQuery.set("family", family);
  const result = await api(`/insights?${insightQuery}`);
  const insights = result.insights || [];
  setRoot(shell("Insights", `
    <section class="insights-heading">
      <div><p class="lede">Patterns grounded in your own sources, with the evidence kept close.</p><p class="meta">Local detectors work without a model provider. Feedback shapes what returns.</p></div>
      <label for="insight-family">Pattern family<select id="insight-family">
        ${libraryFilterOptions(["emerging_theme", "recurring_connection", "changed_thinking", "knowledge_gap", "forgotten_value", "serendipitous_connection"], family, "All insights")}
      </select></label>
    </section>
    <section class="insight-list" aria-live="polite">
      ${insights.map(insightCard).join("") || insightEmptyState(result.state, family)}
    </section>
    ${result.next_cursor ? `<button type="button" class="secondary" data-insight-more data-cursor="${escapeHTML(result.next_cursor)}">Load more</button>` : ""}
  `));
  document.querySelector("#insight-family")?.addEventListener("change", (event) => navigate(`/insights${event.currentTarget.value ? `?family=${encodeURIComponent(event.currentTarget.value)}` : ""}`));
  bindInsightActions();
  recordInsightImpressions(insights);
}

function insightEmptyState(state, family) {
  if (state === "not_enough_history") return emptyState({ eyebrow: "Not enough history", title: "Insights need a little history", body: "Keep capturing and connecting. Arivu will surface patterns only when your own evidence supports them.", tag: "section" });
  if (state === "reprocessing_required") return emptyState({ eyebrow: "Processing needed", title: "Refresh your saved sources", body: "Some items need to be reprocessed before Arivu can derive trustworthy patterns.", tag: "section" });
  return emptyState({ eyebrow: "No qualifying patterns", title: family ? "No patterns in this family" : "No insights yet", body: "Arivu did not find a specific, evidence-backed pattern for this view.", tag: "section" });
}

function insightCard(insight) {
  const confidence = Math.round(Number(insight.confidence || 0) * 100);
  const evidence = insight.evidence || [];
  const isRecommendation = insight.kind === "recommendation";
  return `<article class="insight-card" data-insight-id="${escapeHTML(insight.id)}">
    <header><div><p class="meta">${isRecommendation ? "Recommendation · " : ""}${escapeHTML(knowledgeTypeLabel(insight.type))} · ${escapeHTML(insight.window || "current")}</p><h2>${escapeHTML(insight.title || "Knowledge pattern")}</h2></div>${isRecommendation ? "" : `<span class="confidence" title="Evidence strength">${escapeHTML(insight.evidence_strength || `${confidence}% confidence`)}</span>`}</header>
    <p class="insight-explanation">${escapeHTML(insight.explanation || "")}</p>
    <details><summary>Why Arivu detected this</summary><p>${escapeHTML(insight.why_detected || "Detected from the evidence below.")}</p></details>
    <div class="evidence-list" aria-label="Supporting evidence">
      ${evidence.map((item) => `<a href="${knowledgeItemHref(item.type, item.id, item.title)}"><span>${escapeHTML(item.title || item.id)}</span><small>${escapeHTML(knowledgeTypeLabel(item.type))}</small></a>`).join("")}
    </div>
    <div class="insight-actions">
      ${(insight.actions || []).slice(0, 2).map((action) => insightNextAction(action, evidence[0])).join("")}
      <span class="action-spacer"></span>
      <button type="button" class="secondary" data-insight-feedback="useful">Useful</button>
      <button type="button" class="secondary" data-insight-feedback="not_useful">Not useful</button>
      <select data-insight-reason aria-label="Why this insight was not useful"><option value="">Reason (optional)</option><option value="unsupported">Unsupported</option><option value="obvious">Obvious</option><option value="generic">Too generic</option><option value="wrong_connection">Wrong connection</option><option value="stale">Stale</option><option value="bad_source">Bad source</option></select>
      <button type="button" class="secondary" data-insight-feedback="snooze">Snooze</button>
      <button type="button" class="secondary" data-insight-feedback="dismiss">Dismiss</button>
    </div>
  </article>`;
}

function insightNextAction(action, evidence) {
  if (action === "create_note") return `<button type="button" data-insight-next="capture-note">Create note</button>`;
  if (action === "connect" && evidence) return `<a class="button" href="/graph?focus=${encodeURIComponent(`${evidence.type}:${evidence.id}`)}">Explore connection</a>`;
  if (action === "snooze") return "";
  return evidence ? `<a class="button" href="${knowledgeItemHref(evidence.type, evidence.id, evidence.title)}">Review evidence</a>` : "";
}

function bindInsightActions() {
  document.querySelectorAll("[data-insight-feedback]:not([data-bound])").forEach((button) => {
    button.dataset.bound = "true";
    button.addEventListener("click", async () => {
    const card = button.closest("[data-insight-id]");
    const done = setButtonBusy(button, "Saving");
    try {
      const reason = button.dataset.insightFeedback === "not_useful" ? card.querySelector("[data-insight-reason]")?.value || "" : "";
      await api("/feedback", { method: "POST", body: JSON.stringify({ target_type: "insight", target_id: card.dataset.insightId, feedback: button.dataset.insightFeedback, reason }) });
      ui.toast("Insight feedback saved", "success");
      if (button.dataset.insightFeedback === "dismiss" || button.dataset.insightFeedback === "snooze") card.remove();
    } catch (err) { ui.toast(err.message, "error"); } finally { done(); }
    });
  });
  document.querySelectorAll("[data-insight-next='capture-note']:not([data-bound])").forEach((button) => { button.dataset.bound = "true"; button.addEventListener("click", openCaptureComposer); });
  const moreButton = document.querySelector("[data-insight-more]:not([data-bound])");
  if (moreButton) moreButton.dataset.bound = "true";
  moreButton?.addEventListener("click", async (event) => {
    const button = event.currentTarget;
    const family = new URLSearchParams(location.search).get("family") || "";
    const query = new URLSearchParams({ limit: "40", cursor: button.dataset.cursor });
    if (family) query.set("family", family);
    const done = setButtonBusy(button, "Loading");
    try {
      const result = await api(`/insights?${query}`);
      if (result.restart_required) { ui.toast("Your library changed. Refreshing insights.", "info"); return insightsPage(); }
      const items = result.insights || [];
      document.querySelector(".insight-list")?.insertAdjacentHTML("beforeend", items.map(insightCard).join(""));
      recordInsightImpressions(items);
      button.dataset.cursor = result.next_cursor || "";
      if (!result.next_cursor) button.remove(); else { done(); bindInsightActions(); }
    } catch (err) { ui.toast(err.message, "error"); done(); }
  });
}

function recordInsightImpressions(insights) {
  const target_ids = insights.map((insight) => insight.id).filter(Boolean);
  if (!target_ids.length) return;
  queueMicrotask(() => api("/feedback", { method: "POST", body: JSON.stringify({ target_type: "insight_impression", target_ids }) }).catch(() => {}));
}

async function adminPage() {
  await requireUser();
  const params = new URLSearchParams(location.search);
  const active = params.get("section") || "overview";
  const sort = params.get("sort") || "created_at";
  const order = params.get("order") || "desc";
  const [overview, usage, users, system, activity, collections, settings, audit] = await Promise.all([
    api("/admin/overview"),
    api("/admin/api-usage"),
    api(`/admin/users?sort=${encodeURIComponent(sort)}&order=${encodeURIComponent(order)}`),
    api("/admin/system"),
    api("/admin/activity"),
    api("/admin/collections-stats"),
    api("/admin/settings"),
    api("/admin/audit-events?limit=12").catch(() => ({ events: [] })),
  ]);
  const tabs = [
    ["overview", "Overview"],
    ["api", "API Usage"],
    ["users", "Users"],
    ["system", "System"],
    ["activity", "Activity"],
    ["collections", "Collections"],
    ["settings", "Settings"],
    ["audit", "Audit"],
  ];
  const selected = tabs.some(([id]) => id === active) ? active : "overview";
  setRoot(shell("Admin", `<section class="panel tabs" id="admin-tabs">
    <div class="tab-list" role="tablist" aria-label="Admin sections">
      ${tabs.map(([id, label]) => `<button type="button" role="tab" id="tab-${id}" aria-controls="panel-${id}" aria-selected="${id === selected}">${label}</button>`).join("")}
    </div>
    <div role="tabpanel" id="panel-overview" aria-labelledby="tab-overview">${adminOverviewPanel(overview)}</div>
    <div role="tabpanel" id="panel-api" aria-labelledby="tab-api">${adminUsagePanel(usage)}</div>
    <div role="tabpanel" id="panel-users" aria-labelledby="tab-users">${adminUsersPanel(users, sort, order)}</div>
    <div role="tabpanel" id="panel-system" aria-labelledby="tab-system">${adminSystemPanel(system)}</div>
    <div role="tabpanel" id="panel-activity" aria-labelledby="tab-activity">${adminActivityPanel(activity)}</div>
    <div role="tabpanel" id="panel-collections" aria-labelledby="tab-collections">${adminCollectionsPanel(collections)}</div>
    <div role="tabpanel" id="panel-settings" aria-labelledby="tab-settings">${adminSettingsPanel(settings)}</div>
    <div role="tabpanel" id="panel-audit" aria-labelledby="tab-audit"><section class="stack">${auditEvents(audit.events || [])}</section></div>
  </section>`));
  ui.tabs(document.querySelector("#admin-tabs"));
  bindAdminUsersPanel();
  bindAdminSettingsPanel();
}

function adminOverviewPanel(data) {
  return `<section class="grid compact-grid">
    ${adminStat("Users", data.users?.total, `Today ${formatCount(data.users?.today)} · Week ${formatCount(data.users?.this_week)} · Month ${formatCount(data.users?.this_month)}`)}
    ${adminStat("Bookmarks", data.bookmarks?.total, `Today ${formatCount(data.bookmarks?.today)} · Week ${formatCount(data.bookmarks?.this_week)} · Month ${formatCount(data.bookmarks?.this_month)}`)}
    ${adminStat("Collections", data.collections?.total, "User-owned collections")}
    ${adminStat("AI summaries", data.ai_summaries?.total, "Summary rows")}
    ${adminStat("Avg saves/user", Number(data.bookmarks?.avg_per_user || 0).toFixed(1), "Bookmark density")}
    ${adminStat("Uptime", formatUptime(data.server?.uptime_seconds), data.server?.started_at || "")}
    ${adminStat("SQLite", formatBytes(data.sqlite?.size_bytes), data.sqlite?.path || "")}
    ${adminStat("WAL", formatBytes(data.sqlite?.wal_size_bytes), "Current write-ahead log")}
  </section>`;
}

function adminUsagePanel(data) {
  const ops = data.provider_usage?.ai || data.provider_usage?.gemini || {};
  return `<section class="grid compact-grid">
    ${adminStat("AI calls", data.requests_today, data.ai_configured || data.gemini_configured ? "Configured" : "Not configured")}
    ${adminStat("AI errors", data.provider_usage?.errors_total || 0, data.provider_usage?.since || "")}
    ${adminStat("Summaries done", data.summaries_completed, `Pending ${formatCount(data.summaries_pending)} · Failed ${formatCount(data.summaries_failed)}`)}
    ${adminStat("Jobs queued", data.background_jobs_queued, `Running ${formatCount(data.background_jobs_running)} · Failed ${formatCount(data.background_jobs_failed)}`)}
  </section>
  <section class="stack">${Object.entries(ops).map(([name, item]) => `<article class="annotation">
    <p><strong>${escapeHTML(name)}</strong> <span class="meta">${formatCount(item.requests)} calls · ${formatCount(item.errors)} errors</span></p>
    ${item.last_error ? `<p class="meta">${escapeHTML(item.last_error)}</p>` : ""}
  </article>`).join("") || `<p class="meta">No AI calls recorded for this process.</p>`}</section>`;
}

function adminUsersPanel(users, sort, order) {
  return `<form class="toolbar" id="admin-user-sort">
    <select id="admin-user-sort-field" aria-label="Sort users">
      ${["created_at", "email", "name", "bookmarks", "last_bookmark_at"].map((value) => `<option value="${value}"${value === sort ? " selected" : ""}>${value.replaceAll("_", " ")}</option>`).join("")}
    </select>
    <select id="admin-user-sort-order" aria-label="Sort order">
      <option value="desc"${order !== "asc" ? " selected" : ""}>Descending</option>
      <option value="asc"${order === "asc" ? " selected" : ""}>Ascending</option>
    </select>
    <button type="submit" class="secondary">Sort</button>
  </form>
  <form class="panel form" id="admin-invite-form">
    <h3>Invite user</h3>
    <div class="field"><label for="admin-invite-email">Email</label><input id="admin-invite-email" type="email" required></div>
    <div class="field"><label for="admin-invite-name">Name</label><input id="admin-invite-name" type="text"></div>
    <p class="form-message" data-form-message hidden></p>
    <button type="submit">Invite</button>
  </form>
  <div class="table-wrap"><table class="data-table">
    <thead><tr><th>Name</th><th>Email</th><th>Bookmarks</th><th>Collections</th><th>Joined</th><th>Last save</th><th>Status</th><th>Actions</th></tr></thead>
    <tbody>${(users || []).map(adminUserRow).join("")}</tbody>
  </table></div>`;
}

function adminUserRow(user) {
  const action = user.banned ? "unban" : "ban";
  return `<tr>
    <td>${escapeHTML(user.name || "")}</td>
    <td>${escapeHTML(user.email || "")}</td>
    <td>${formatCount(user.bookmark_count)}</td>
    <td>${formatCount(user.collection_count)}</td>
    <td>${formatDate(user.created_at)}</td>
    <td>${formatDate(user.last_bookmark_at)}</td>
    <td>${user.is_admin ? "Admin" : user.invite_pending ? "Invited" : user.banned ? "Banned" : "Active"}</td>
    <td><div class="button-row">
      <button type="button" class="secondary" data-admin-user-detail="${escapeHTML(user.id)}">View details</button>
      <button type="button" class="secondary" data-admin-user-action="${action}" data-user-id="${escapeHTML(user.id)}">${action === "ban" ? "Ban user" : "Unban user"}</button>
      <button type="button" class="secondary" data-admin-user-action="reset-password" data-user-id="${escapeHTML(user.id)}">Reset password</button>
      <button type="button" class="danger" data-admin-user-action="delete" data-user-id="${escapeHTML(user.id)}">Delete user</button>
    </div></td>
  </tr>`;
}

function adminSystemPanel(data) {
  return `<section class="grid compact-grid">
    ${adminStat("Go", data.system?.go || "", `${formatCount(data.system?.goroutines)} goroutines`)}
    ${adminStat("Alloc", formatBytes(data.system?.alloc_bytes), `Heap ${formatBytes(data.system?.heap_alloc_bytes)}`)}
    ${adminStat("SQLite", formatBytes(data.sqlite?.size_bytes), data.sqlite?.path || "")}
    ${adminStat("Open conns", data.db?.OpenConnections || 0, `In use ${formatCount(data.db?.InUse)} · Idle ${formatCount(data.db?.Idle)}`)}
  </section>
  <div class="table-wrap"><table class="data-table">
    <thead><tr><th>Table</th><th>Rows</th></tr></thead>
    <tbody>${Object.entries(data.tables || {}).map(([name, item]) => `<tr><td>${escapeHTML(name)}</td><td>${formatCount(item.count)}</td></tr>`).join("")}</tbody>
  </table></div>`;
}

function adminActivityPanel(data) {
  return `<section class="split">
    <section class="panel"><h3>Recent bookmarks</h3><div class="stack">${(data.recent_bookmarks || []).map((item) => `<article class="annotation"><p><strong>${escapeHTML(item.title || item.url || "Untitled")}</strong> <span class="meta">${escapeHTML(item.user_email || "")}</span></p><p class="meta">${formatDate(item.created_at)} · ${escapeHTML(item.domain || "")}</p></article>`).join("") || `<p class="meta">No bookmarks yet.</p>`}</div></section>
    <section class="panel"><h3>Recent registrations</h3><div class="stack">${(data.recent_registrations || []).map((item) => `<article class="annotation"><p><strong>${escapeHTML(item.email || "")}</strong></p><p class="meta">${formatDate(item.created_at)} · ${escapeHTML(item.name || "")}</p></article>`).join("") || `<p class="meta">No users yet.</p>`}</div></section>
  </section>`;
}

function adminCollectionsPanel(items) {
  return `<div class="table-wrap"><table class="data-table">
    <thead><tr><th>Collection</th><th>Owner</th><th>Bookmarks</th><th>Latest add</th><th>Created</th></tr></thead>
    <tbody>${(items || []).map((item) => `<tr><td>${escapeHTML(item.name || "")}</td><td>${escapeHTML(item.user_email || "")}</td><td>${formatCount(item.bookmark_count)}</td><td>${formatDate(item.latest_added_at)}</td><td>${formatDate(item.created_at)}</td></tr>`).join("")}</tbody>
  </table></div>`;
}

function adminStat(label, value, meta) {
  return `<article class="panel"><span class="meta">${escapeHTML(label)}</span><h2>${escapeHTML(value === undefined || value === null || value === "" ? "0" : String(value))}</h2>${meta ? `<p class="meta">${escapeHTML(meta)}</p>` : ""}</article>`;
}

function bindAdminUsersPanel() {
  const sortForm = document.querySelector("#admin-user-sort");
  sortForm?.addEventListener("submit", (event) => {
    event.preventDefault();
    navigate(`/admin?section=users&sort=${encodeURIComponent(document.querySelector("#admin-user-sort-field").value)}&order=${encodeURIComponent(document.querySelector("#admin-user-sort-order").value)}`);
  });
  const inviteForm = document.querySelector("#admin-invite-form");
  inviteForm?.addEventListener("submit", async (event) => {
    event.preventDefault();
    const done = setButtonBusy(event.submitter, "Inviting");
    setFormMessage(inviteForm);
    try {
      await api("/admin/users/invite", { method: "POST", body: JSON.stringify({ email: document.querySelector("#admin-invite-email").value, name: document.querySelector("#admin-invite-name").value }) });
      ui.toast("Invite created", "success");
      render();
    } catch (err) {
      setFormMessage(inviteForm, err.message);
      ui.toast(err.message, "error");
    } finally {
      done();
    }
  });
  document.querySelectorAll("[data-admin-user-detail]").forEach((button) => button.addEventListener("click", () => showAdminUser(button.dataset.adminUserDetail)));
  document.querySelectorAll("[data-admin-user-action]").forEach((button) => button.addEventListener("click", () => runAdminUserAction(button)));
}

async function showAdminUser(userID) {
  try {
    const user = await api(`/admin/users/${userID}`);
    const body = document.createElement("div");
    body.className = "stack";
    body.innerHTML = `<p><strong>${escapeHTML(user.email || "")}</strong> <span class="meta">${escapeHTML(user.name || "")}</span></p>
      <p class="meta">${formatCount(user.bookmark_count)} bookmarks · ${formatCount(user.collection_count)} collections · ${user.banned ? "Banned" : "Active"}</p>
      ${(user.recent_bookmarks || []).map((item) => `<p class="meta">${formatDate(item.created_at)} · ${escapeHTML(item.title || item.url || "Untitled")}</p>`).join("")}`;
    await ui.dialog({ title: "User detail", body, actions: [{ label: "Close", value: true, kind: "secondary" }] });
  } catch (err) {
    ui.toast(err.message, "error");
  }
}

async function runAdminUserAction(button) {
  const action = button.dataset.adminUserAction;
  const userID = button.dataset.userId;
  if (action === "delete") {
    const ok = await ui.confirmDestructive({ title: "Delete user", body: "Delete this user and all of their bookmarks, notes, collections, sessions, and account data? This cannot be undone.", confirm: "Delete user permanently", cancel: "Keep user" });
    if (!ok) return;
  }
  let body = "{}";
  if (action === "reset-password") {
    const password = await requestAdminResetPassword();
    if (!password) return;
    body = JSON.stringify({ new_password: password });
  }
  const done = setButtonBusy(button, "Working");
  try {
    const method = action === "delete" ? "DELETE" : "POST";
    const path = action === "delete" ? `/admin/users/${userID}` : `/admin/users/${userID}/${action}`;
    await api(path, { method, body: method === "DELETE" ? undefined : body });
    ui.toast("User updated", "success");
    render();
  } catch (err) {
    ui.toast(err.message, "error");
  } finally {
    done();
  }
}

async function requestAdminResetPassword() {
  const body = document.createElement("div");
  body.className = "form";
  body.innerHTML = `
    <p class="meta">Set a temporary password for this account. Share it through a private channel.</p>
    <div class="field">
      <label for="admin-reset-password">New password</label>
      <input id="admin-reset-password" type="password" autocomplete="new-password" minlength="8" required>
    </div>
  `;
  const ok = await ui.dialog({
    title: "Reset password",
    body,
    actions: [
      { label: "Keep current password", value: false, kind: "secondary" },
      { label: "Reset password", value: true, kind: "danger", beforeClose: () => body.querySelector("#admin-reset-password").reportValidity() },
    ],
  });
  return ok ? body.querySelector("#admin-reset-password")?.value.trim() || "" : "";
}

function formatCount(value) {
  return Number(value || 0).toLocaleString();
}

function formatBytes(value) {
  const bytes = Number(value || 0);
  if (!bytes) return "0 B";
  const units = ["B", "KB", "MB", "GB"];
  let size = bytes;
  let unit = 0;
  while (size >= 1024 && unit < units.length - 1) {
    size /= 1024;
    unit++;
  }
  return `${size.toFixed(unit ? 1 : 0)} ${units[unit]}`;
}

function formatUptime(seconds) {
  const total = Number(seconds || 0);
  const days = Math.floor(total / 86400);
  const hours = Math.floor((total % 86400) / 3600);
  const minutes = Math.floor((total % 3600) / 60);
  return days ? `${days}d ${hours}h` : hours ? `${hours}h ${minutes}m` : `${minutes}m`;
}

function formatDate(value) {
  if (!value) return "";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return String(value);
  return date.toLocaleString();
}

function auditEvents(events) {
  if (!events.length) return `<p class="meta">No audit events recorded yet.</p>`;
  return events.map((event) => `<article class="annotation">
    <p><strong>${escapeHTML(event.action || "event")}</strong> <span class="meta">${escapeHTML(event.created_at || "")}</span></p>
    <p class="meta">${escapeHTML(event.actor_email || event.actor_id || "system")} · ${escapeHTML(event.target_type || "target")}${event.target_id ? `:${escapeHTML(event.target_id)}` : ""}</p>
    ${auditMetadata(event.metadata)}
  </article>`).join("");
}

function auditMetadata(metadata) {
  if (!metadata || !Object.keys(metadata).length) return "";
  return `<p class="meta">${escapeHTML(JSON.stringify(metadata))}</p>`;
}

async function requireUser() {
  if (state.user) return state.user;
  try {
    state.user = await api("/auth/me");
    flushOfflineBookmarks({ quiet: true });
    return state.user;
  } catch {
    navigate("/auth", true);
    throw new Error("auth required");
  }
}

async function render() {
  state.pendingRoutes += 1;
  document.body.classList.add("is-routing");
  const route = routes.find(routeMatches);
  const page = route ? route.page : location.pathname === "/" ? () => navigate(state.user ? "/today" : "/auth", true) : dashboardPage;
  try {
    if (route?.access === "protected") await requireUser();
    await page();
    syncRouteAccessibility();
    bindGlobalShellActions();
    ui.on(document, "keydown", (event) => {
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "k") {
        event.preventDefault();
        openCommandPalette();
      }
    });
    document.querySelector("#logout")?.addEventListener("click", async (event) => {
      const done = setButtonBusy(event.currentTarget, "Signing out");
      await api("/auth/logout", { method: "POST" }).catch(() => {});
      state.user = null;
      ui.toast("Signed out", "success");
      navigate("/auth", true);
      done();
    });
  } catch (err) {
    if (err.message !== "auth required") ui.toast(err.message, "error");
  } finally {
    state.pendingRoutes = Math.max(0, state.pendingRoutes - 1);
    if (state.pendingRoutes === 0) document.body.classList.remove("is-routing");
  }
}

function bindGlobalShellActions() {
  document.querySelector("#global-capture")?.addEventListener("click", openCaptureComposer);
  document.querySelector("#global-actions")?.addEventListener("click", openCommandPalette);
  const profile = document.querySelector("#profile-menu");
  if (!profile) return;
  const items = [
    { label: "Settings", action: () => navigate("/settings") },
  ];
  if (state.user?.is_admin) items.push({ label: "Administration", action: () => navigate("/admin") });
  items.push({ label: "Log out", action: async () => {
    await api("/auth/logout", { method: "POST" }).catch(() => {});
    state.user = null;
    navigate("/auth", true);
  } });
  ui.menu(profile, items);
}

function routeMatches(route) {
  return location.pathname === route.prefix || (route.prefix.endsWith("/") && location.pathname.startsWith(route.prefix));
}

document.addEventListener("click", (event) => {
  if (!(event.target instanceof Element)) return;
  const link = event.target.closest("a[href^='/']");
  if (!link) return;
  if (event.defaultPrevented || event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
  if (link.target && link.target !== "_self") return;
  event.preventDefault();
  navigate(link.getAttribute("href"));
});
addEventListener("popstate", () => {
  state.focusMainAfterRender = true;
  render();
});
addEventListener("online", () => flushOfflineBookmarks());
registerServiceWorker();
render();
