const state = {
  user: null,
  bookmarks: [],
  collections: [],
  abort: null,
  cleanup: [],
};

const routes = [
  ["/auth", authPage],
  ["/dashboard", dashboardPage],
  ["/bookmark/", bookmarkPage],
  ["/duplicates", simplePage("Duplicates", "Find repeated saves and merge them without losing notes, summaries, or reading history.")],
  ["/settings", settingsPage],
  ["/imports", () => navigate("/settings?section=import", true)],
  ["/knowledge-graph", simplePage("Knowledge Graph", "Explore the ideas, sources, and connections that recur across your saved pages.")],
  ["/analytics", analyticsPage],
  ["/admin", adminPage],
  ["/reset-password", simplePage("Reset Password", "Choose a new password to get back into your library.")],
  ["/accept-invite", simplePage("Accept Invite", "Set up your account and start saving useful pages.")],
];

async function api(path, options = {}) {
  const headers = new Headers(options.headers || {});
  if (options.body && !headers.has("Content-Type")) headers.set("Content-Type", "application/json");
  const csrf = getCookie("csrf_token");
  if (csrf) headers.set("X-CSRF-Token", csrf);
  const res = await fetch(`/api${path}`, { credentials: "include", ...options, headers });
  if (res.status === 401 && path !== "/auth/me") {
    const refreshed = await fetch("/api/auth/refresh", { method: "POST", credentials: "include", headers: csrf ? { "X-CSRF-Token": csrf } : {} });
    if (refreshed.ok) return api(path, options);
    navigate("/auth", true);
  }
  const type = res.headers.get("Content-Type") || "";
  const data = type.includes("application/json") ? await res.json() : await res.text();
  if (!res.ok) throw Object.assign(new Error(data.detail || "Request failed"), { status: res.status, data });
  return data;
}

function getCookie(name) {
  return document.cookie.split("; ").find((row) => row.startsWith(`${name}=`))?.split("=")[1] || "";
}

function html(strings, ...values) {
  return strings.reduce((out, str, i) => out + str + escapeHTML(values[i] ?? ""), "");
}

function escapeHTML(value) {
  return String(value).replace(/[&<>"']/g, (ch) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[ch]);
}

function setRoot(markup) {
  disposeRoute();
  const root = document.querySelector("#app");
  root.innerHTML = markup;
}

function toast(message) {
  const region = document.querySelector("#toast-region");
  const item = document.createElement("div");
  item.className = "toast";
  item.setAttribute("role", "status");
  item.textContent = message;
  region.append(item);
  setTimeout(() => item.remove(), 3200);
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
          <button type="button" class="icon-button" data-dialog-close aria-label="Close dialog">x</button>
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
      button.addEventListener("click", () => close(action.value));
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
  confirmDestructive({ title, body, confirm = "Delete" }) {
    return ui.dialog({
      title,
      body,
      actions: [
        { label: "Cancel", value: false, kind: "secondary" },
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
  history[replace ? "replaceState" : "pushState"]({}, "", path);
  render();
}

function shell(title, content) {
  const nav = [
    ["/dashboard", "Bookmarks"],
    ["/knowledge-graph", "Graph"],
    ["/analytics", "Analytics"],
    ["/duplicates", "Duplicates"],
    ["/settings", "Settings"],
    ["/admin", "Admin"],
  ];
  return `
    <div class="shell">
      <aside class="sidebar">
        <p class="brand">Arivu</p>
        <nav class="nav" aria-label="Primary">
          ${nav.map(([href, label]) => `<a href="${href}" class="${location.pathname.startsWith(href) ? "active" : ""}">${label}</a>`).join("")}
        </nav>
      </aside>
      <main class="main">
        <div class="topbar">
          <h1 class="headline">${escapeHTML(title)}</h1>
          <div class="top-actions">
            <button id="global-actions" class="secondary" type="button">Actions</button>
            <button id="logout" class="secondary" type="button">Log out</button>
          </div>
        </div>
        ${content}
      </main>
    </div>
  `;
}

async function authPage() {
  setRoot(html`
    <main class="auth">
      <section class="panel">
        <h1>Arivu</h1>
        <p class="meta">Save the web pages worth remembering, then turn them into summaries, signals, and connections.</p>
        <form class="form" id="login-form">
          <div class="field"><label for="email">Email</label><input id="email" type="email" required autocomplete="email"></div>
          <div class="field"><label for="password">Password</label><input id="password" type="password" required autocomplete="current-password"></div>
          <button type="submit">Log in</button>
          <button type="button" class="secondary" id="signup">Create account</button>
        </form>
      </section>
    </main>
  `);
  document.querySelector("#login-form").addEventListener("submit", async (event) => {
    event.preventDefault();
    const body = JSON.stringify({ email: email.value, password: password.value });
    try {
      await api("/auth/login", { method: "POST", body });
      ui.toast("Signed in");
      navigate("/dashboard", true);
    } catch (err) { ui.toast(err.message); }
  });
  document.querySelector("#signup").addEventListener("click", async () => {
    try {
      await api("/auth/signup", { method: "POST", body: JSON.stringify({ email: email.value, password: password.value }) });
      ui.toast("Account created");
      navigate("/dashboard", true);
    } catch (err) { ui.toast(err.message); }
  });
}

async function dashboardPage() {
  await requireUser();
  const bookmarks = await api(`/bookmarks${location.search}`) || [];
  state.bookmarks = bookmarks;
  setRoot(shell("Saved knowledge", `
    <form class="panel form" id="save-form">
      <div class="field"><label for="url">Save URL</label><input id="url" type="url" placeholder="https://example.com/article" required></div>
      <button type="submit">Save bookmark</button>
    </form>
    <div class="toolbar" role="search">
      <input id="search" type="search" placeholder="Search bookmarks" value="${escapeHTML(new URLSearchParams(location.search).get("search") || "")}">
      <button id="search-button" class="secondary">Search</button>
    </div>
    <section class="grid" aria-label="Bookmarks">
      ${bookmarks.map(bookmarkCard).join("") || `<div class="panel"><h2>No bookmarks yet</h2><p>Save a URL to start building your knowledge graph.</p></div>`}
    </section>
  `));
  document.querySelector("#save-form").addEventListener("submit", async (event) => {
    event.preventDefault();
    try {
      await api("/bookmarks", { method: "POST", body: JSON.stringify({ url: document.querySelector("#url").value }) });
      ui.toast("Bookmark saved");
      render();
    } catch (err) { ui.toast(err.message); }
  });
  document.querySelector("#search-button").addEventListener("click", () => {
    const value = document.querySelector("#search").value.trim();
    navigate(`/dashboard${value ? `?search=${encodeURIComponent(value)}` : ""}`);
  });
}

function bookmarkCard(b) {
  return html`<a class="panel bookmark" href="/bookmark/${b.id}">
    <span class="meta">${b.domain || "web"} · ${b.reading_time || 0} min</span>
    <h2>${b.title || b.url}</h2>
    <p>${b.description || "Queued for enrichment"}</p>
  </a>`;
}

async function bookmarkPage() {
  await requireUser();
  const id = location.pathname.split("/").pop();
  const bookmark = await api(`/bookmarks/${id}`);
  setRoot(shell(bookmark.title || "Bookmark", `
    <article class="panel reader">
      <p class="meta">${bookmark.domain || ""} · ${bookmark.reading_time || 0} min</p>
      <p class="button-row"><a class="button" href="${escapeHTML(bookmark.url)}" target="_blank" rel="noreferrer noopener">Open original</a><button type="button" class="danger" id="delete-bookmark">Delete</button></p>
      <div class="reader-content">${bookmark.html_content || `<p>${escapeHTML(bookmark.text_content || bookmark.description || "No archived text yet.")}</p>`}</div>
    </article>
  `));
  document.querySelector("#delete-bookmark").addEventListener("click", async () => {
    const confirmed = await ui.confirmDestructive({ title: "Delete bookmark", body: "This removes the bookmark, summary, graph terms, and collection links.", confirm: "Delete bookmark" });
    if (!confirmed) return;
    try {
      await api(`/bookmarks/${id}`, { method: "DELETE" });
      ui.toast("Bookmark deleted");
      navigate("/dashboard", true);
    } catch (err) { ui.toast(err.message); }
  });
}

async function settingsPage() {
  await requireUser();
  const section = new URLSearchParams(location.search).get("section") || "profile";
  const tabs = [
    ["profile", "Profile", "Manage your profile and account access."],
    ["import", "Import", "Bring in browser, Pocket, Raindrop, or URL-list exports."],
    ["connections", "Connections", "Connect provider accounts and sync saved items."],
    ["api-keys", "API Keys", "Configure provider keys for enrichment and delivery."],
  ];
  const active = tabs.some(([id]) => id === section) ? section : "profile";
  setRoot(shell("Settings", `<section class="panel tabs" id="settings-tabs">
    <div class="tab-list" role="tablist" aria-label="Settings sections">
      ${tabs.map(([id, label]) => `<button type="button" role="tab" id="tab-${id}" aria-controls="panel-${id}" aria-selected="${id === active}">${label}</button>`).join("")}
    </div>
    ${tabs.map(([id, label, copy]) => `<div role="tabpanel" id="panel-${id}" aria-labelledby="tab-${id}"><h2>${label}</h2><p>${copy}</p></div>`).join("")}
  </section>`));
  ui.tabs(document.querySelector("#settings-tabs"));
}

async function analyticsPage() {
  await requireUser();
  const summary = await api("/analytics/summary");
  setRoot(shell("Analytics", `<section class="grid">
    <div class="panel"><span class="meta">Bookmarks</span><h2>${summary.total_bookmarks || 0}</h2></div>
    <div class="panel"><span class="meta">Collections</span><h2>${summary.total_collections || 0}</h2></div>
  </section>`));
}

async function adminPage() {
  await requireUser();
  const overview = await api("/admin/overview");
  setRoot(shell("Admin", `<section class="grid">
    <div class="panel"><span class="meta">Users</span><h2>${overview.users?.total || 0}</h2></div>
    <div class="panel"><span class="meta">Bookmarks</span><h2>${overview.bookmarks?.total || 0}</h2></div>
    <div class="panel"><span class="meta">Database</span><h2>SQLite</h2></div>
  </section>`));
}

function simplePage(title, copy) {
  return async () => {
    await requireUser().catch(() => {});
    setRoot(shell(title, `<section class="panel"><p>${escapeHTML(copy)}</p></section>`));
  };
}

async function requireUser() {
  if (state.user) return state.user;
  try {
    state.user = await api("/auth/me");
    return state.user;
  } catch {
    navigate("/auth", true);
    throw new Error("auth required");
  }
}

async function render() {
  if (state.abort) state.abort.abort();
  state.abort = new AbortController();
  const route = routes.find(([prefix]) => location.pathname === prefix || (prefix.endsWith("/") && location.pathname.startsWith(prefix)));
  const page = route ? route[1] : location.pathname === "/" ? () => navigate(state.user ? "/dashboard" : "/auth", true) : dashboardPage;
  try {
    await page();
    const actions = document.querySelector("#global-actions");
    if (actions) {
      ui.menu(actions, [
        { label: "Dashboard", action: () => navigate("/dashboard") },
        { label: "Settings", action: () => navigate("/settings") },
        { label: "Admin", action: () => navigate("/admin") },
      ]);
    }
    document.querySelector("#logout")?.addEventListener("click", async () => {
      await api("/auth/logout", { method: "POST" }).catch(() => {});
      state.user = null;
      navigate("/auth", true);
    });
  } catch (err) {
    if (err.message !== "auth required") ui.toast(err.message);
  }
}

document.addEventListener("click", (event) => {
  const link = event.target.closest("a[href^='/']");
  if (!link) return;
  event.preventDefault();
  navigate(link.getAttribute("href"));
});
addEventListener("popstate", render);
render();
