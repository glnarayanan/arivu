const state = {
  user: null,
  cleanup: [],
  pendingRoutes: 0,
};

const routes = [
  { prefix: "/auth", page: authPage, access: "public" },
  { prefix: "/reset-password", page: resetPasswordPage, access: "public" },
  { prefix: "/accept-invite", page: acceptInvitePage, access: "public" },
  { prefix: "/dashboard", page: dashboardPage, access: "protected" },
  { prefix: "/bookmark/", page: bookmarkPage, access: "protected" },
  { prefix: "/notes", page: notesPage, access: "protected" },
  { prefix: "/review", page: reviewPage, access: "protected" },
  { prefix: "/duplicates", page: duplicatesPage, access: "protected" },
  { prefix: "/settings", page: settingsPage, access: "protected" },
  { prefix: "/imports", page: () => navigate("/settings?section=import", true), access: "protected" },
  { prefix: "/knowledge-graph", page: graphPage, access: "protected" },
  { prefix: "/analytics", page: analyticsPage, access: "protected" },
  { prefix: "/admin", page: adminPage, access: "protected" },
];

async function api(path, options = {}) {
  const { retried = false, ...requestOptions } = options;
  const headers = new Headers(requestOptions.headers || {});
  if (requestOptions.body && !headers.has("Content-Type")) headers.set("Content-Type", "application/json");
  const csrf = getCookie("csrf_token");
  if (csrf) headers.set("X-CSRF-Token", csrf);
  let res;
  try {
    res = await fetch(`/api${path}`, { credentials: "include", ...requestOptions, headers });
  } catch {
    throw new Error("We couldn't reach Arivu. Check your connection and try again.");
  }
  if (res.status === 401 && !path.startsWith("/auth/") && !retried) {
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

function toast(message, tone = "info") {
  const region = document.querySelector("#toast-region");
  const item = document.createElement("div");
  const safeTone = tone === "success" || tone === "error" ? tone : "info";
  item.className = `toast toast-${safeTone}`;
  item.setAttribute("role", safeTone === "error" ? "alert" : "status");
  item.setAttribute("aria-live", safeTone === "error" ? "assertive" : "polite");
  item.textContent = message;
  region.append(item);
  setTimeout(() => item.remove(), 3200);
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
  history[replace ? "replaceState" : "pushState"]({}, "", path);
  render();
}

function shell(title, content) {
  const nav = [
    ["/dashboard", "Bookmarks"],
    ["/notes", "Notes"],
    ["/review", "Review"],
    ["/knowledge-graph", "Graph"],
    ["/analytics", "Analytics"],
    ["/duplicates", "Duplicates"],
    ["/settings", "Settings"],
    ["/admin", "Admin"],
  ];
  return `
    <a class="skip-link" href="#main-content">Skip to content</a>
    <div class="shell">
      <aside class="sidebar">
        <p class="brand">Arivu</p>
        <nav class="nav" aria-label="Primary">
          ${nav.map(([href, label]) => {
            const active = location.pathname.startsWith(href) || (href === "/dashboard" && location.pathname.startsWith("/bookmark/"));
            return `<a href="${href}"${active ? ` class="active" aria-current="page"` : ""}>${label}</a>`;
          }).join("")}
        </nav>
      </aside>
      <main class="main" id="main-content" tabindex="-1">
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
      navigate("/dashboard", true);
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
      navigate("/dashboard", true);
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
  setRoot(html`
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
      navigate("/dashboard", true);
    } catch (err) {
      setFormMessage(form, err.message);
      ui.toast(err.message, "error");
    } finally {
      done();
    }
  });
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
  setRoot(shell("Saved knowledge", `
    <form class="panel form" id="save-form">
      <div class="field"><label for="url">Save URL</label><input id="url" type="url" placeholder="https://example.com/article" value="${escapeHTML(shared.url)}" required></div>
      <div class="field"><label for="save-note">Quick note</label><textarea id="save-note" rows="2" placeholder="Why this matters, optional">${escapeHTML(shared.note)}</textarea></div>
      <div class="field"><label for="save-tags">Tags</label><input id="save-tags" type="text" placeholder="research, idea, later"></div>
      <button type="submit">Save bookmark</button>
      <p class="meta" id="job-status" hidden></p>
    </form>
    <form class="toolbar" role="search" id="search-form">
      <label class="sr-only" for="search">Search bookmarks</label>
      <input id="search" type="search" placeholder="Search bookmarks" value="${escapeHTML(params.get("search") || "")}">
      <input id="filter-tag" type="text" placeholder="Tag" value="${escapeHTML(params.get("tag") || "")}">
      <input id="filter-domain" type="text" placeholder="Domain" value="${escapeHTML(params.get("domain") || "")}">
      <input id="filter-source" type="text" placeholder="Source" value="${escapeHTML(params.get("source") || "")}">
      <input id="filter-date-from" type="date" aria-label="Saved after" value="${escapeHTML(params.get("date_from") || "")}">
      <select id="filter-read">
        <option value="">Any status</option>
        <option value="unread" ${params.get("read_status") === "unread" ? "selected" : ""}>Unread</option>
        <option value="read" ${params.get("read_status") === "read" ? "selected" : ""}>Read</option>
      </select>
      <button id="search-button" class="secondary" type="submit">Search</button>
      <button id="answer-button" class="secondary" type="button">Answer</button>
    </form>
    <section class="split">
      <form class="panel form" id="saved-search-form">
        <h2>Saved search</h2>
        <div class="field"><label for="saved-search-name">Name</label><input id="saved-search-name" type="text" placeholder="Unread research"></div>
        <p class="form-message" id="saved-search-message" data-form-message hidden></p>
        <button type="submit">Save current search</button>
      </form>
      <section class="panel">
        <h2>Saved searches</h2>
        ${savedSearchList(savedSearches.saved_searches || [])}
      </section>
    </section>
    <section class="panel" id="answer-panel" hidden></section>
    <section class="grid" aria-label="Bookmarks">
      ${bookmarkList.map(bookmarkCard).join("") || `<div class="panel empty-state"><span class="meta">First save</span><h2>No bookmarks yet</h2><p>Save a URL above to start building your searchable reading memory.</p></div>`}
    </section>
  `));
  const saveForm = document.querySelector("#save-form");
  saveForm.insertAdjacentHTML("beforeend", `<p class="form-message" id="save-message" data-form-message hidden></p>`);
  saveForm.addEventListener("submit", async (event) => {
    event.preventDefault();
    const done = setButtonBusy(event.submitter, "Saving bookmark");
    setFormMessage(saveForm);
    try {
      const result = await api("/bookmarks", {
        method: "POST",
        body: JSON.stringify({
          url: document.querySelector("#url").value,
          note: document.querySelector("#save-note").value,
          tags: splitTags(document.querySelector("#save-tags").value),
        }),
      });
      ui.toast("Bookmark saved", "success");
      await showJobStatus(result.job_id);
      navigate(`/bookmark/${result.bookmark.id}`, true);
    } catch (err) {
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
    } catch (err) {
      ui.toast(err.message, "error");
    } finally {
      done();
    }
  });
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

function dashboardFilters() {
  return {
    tag: document.querySelector("#filter-tag")?.value.trim() || "",
    domain: document.querySelector("#filter-domain")?.value.trim() || "",
    source: document.querySelector("#filter-source")?.value.trim() || "",
    date_from: document.querySelector("#filter-date-from")?.value || "",
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
    for (const key of ["tag", "domain", "source", "date_from", "read_status"]) {
      if (filters[key]) params.set(key, filters[key]);
    }
    return `<a class="text-link" href="/dashboard?${params.toString()}">${escapeHTML(item.name)}</a>`;
  }).join("")}</div>`;
}

function answerPanel(answer) {
  const citations = answer.citations || [];
  return `<h2>Cited answer</h2>
    <p>${escapeHTML(answer.answer || "")}</p>
    <div class="stack">${citations.map((item, index) => `<article class="annotation">
      <p><strong>[${index + 1}] ${escapeHTML(item.title || item.url)}</strong> <span class="meta">${escapeHTML(item.type || "bookmark")} · ${escapeHTML(item.domain || "")}</span></p>
      <p>${escapeHTML(item.snippet || "")}</p>
      <a class="text-link" href="${item.type === "note" ? "/notes" : `/bookmark/${escapeHTML(item.id)}`}">Open citation</a>
    </article>`).join("") || `<p class="meta">No citations found.</p>`}</div>`;
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
  return html`<a class="panel bookmark" href="/bookmark/${b.id}">
    <span class="meta">${b.domain || "web"} · ${b.reading_time || 0} min</span>
    <h2>${b.title || b.url}</h2>
    <p>${b.description || "Queued for enrichment"}</p>
  </a>`;
}

function splitTags(value) {
  return String(value || "").split(",").map((tag) => tag.trim()).filter(Boolean).slice(0, 20);
}

function selectedReaderText() {
  const reader = document.querySelector(".reader-content");
  const selection = window.getSelection ? window.getSelection() : null;
  if (!reader || !selection || selection.rangeCount === 0) return "";
  const range = selection.getRangeAt(0);
  const start = range.startContainer.nodeType === Node.ELEMENT_NODE ? range.startContainer : range.startContainer.parentElement;
  const end = range.endContainer.nodeType === Node.ELEMENT_NODE ? range.endContainer : range.endContainer.parentElement;
  if (!start || !end || !reader.contains(start) || !reader.contains(end)) return "";
  return selection.toString().replace(/\s+/g, " ").trim().slice(0, 4000);
}

async function showJobStatus(jobID) {
  const status = document.querySelector("#job-status");
  if (!jobID || !status) return;
  status.hidden = false;
  status.textContent = "Processing saved page";
  for (let i = 0; i < 4; i += 1) {
    await new Promise((resolve) => setTimeout(resolve, 650));
    const job = await api(`/jobs/${jobID}`).catch(() => null);
    if (!job) return;
    status.textContent = `Processing status: ${job.status}`;
    if (job.status === "completed" || job.status === "failed") return;
  }
}

function tagList(tags) {
  if (!tags.length) return "";
  return `<div class="chips">${tags.map((tag) => `<span>${escapeHTML(tag.name || tag)}</span>`).join("")}</div>`;
}

function summaryPanel(summary) {
  const bullets = Array.isArray(summary.bullet_points) ? summary.bullet_points : [];
  const highlights = Array.isArray(summary.highlights) ? summary.highlights : [];
  const tags = Array.isArray(summary.suggested_tags) ? summary.suggested_tags : [];
  if (!summary.one_sentence && !bullets.length && !highlights.length && !tags.length) {
    return `<section class="insight-strip"><span class="meta">Enrichment</span><p>${escapeHTML(summary.processing_status || "Queued")}</p></section>`;
  }
  return `<section class="insight-strip">
    <span class="meta">Summary</span>
    ${summary.one_sentence ? `<p>${escapeHTML(summary.one_sentence)}</p>` : ""}
    ${bullets.length ? `<ul>${bullets.map((item) => `<li>${escapeHTML(item)}</li>`).join("")}</ul>` : ""}
    ${highlights.length ? `<p class="meta">Highlights</p><ul>${highlights.map((item) => `<li>${escapeHTML(item)}</li>`).join("")}</ul>` : ""}
    ${tags.length ? `<div class="chips">${tags.map((tag) => `<span>${escapeHTML(tag)}</span>`).join("")}</div>` : ""}
  </section>`;
}

function annotationList(items) {
  if (!items.length) return `<p class="meta">No annotations yet.</p>`;
  return `<div class="stack">${items.map((item) => `<article class="annotation form" data-annotation="${escapeHTML(item.id)}">
    <div class="field"><label for="annotation-quote-${escapeHTML(item.id)}">Quote</label><textarea id="annotation-quote-${escapeHTML(item.id)}" data-annotation-quote rows="3">${escapeHTML(item.quote || "")}</textarea></div>
    <div class="field"><label for="annotation-note-${escapeHTML(item.id)}">Note</label><textarea id="annotation-note-${escapeHTML(item.id)}" data-annotation-note rows="3">${escapeHTML(item.note || "")}</textarea></div>
    <div class="field"><label for="annotation-tags-${escapeHTML(item.id)}">Tags</label><input id="annotation-tags-${escapeHTML(item.id)}" data-annotation-tags value="${escapeHTML((item.tags || []).join(", "))}"></div>
    <p class="button-row">
      <button type="button" data-annotation-save="${escapeHTML(item.id)}">Save changes</button>
      <button type="button" class="danger" data-annotation-delete="${escapeHTML(item.id)}">Delete</button>
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
  const result = await api("/notes");
  const notes = result.notes || [];
  setRoot(shell("Notes", `
    <section class="split">
      <form class="panel form" id="standalone-note-form">
        <h2>New note</h2>
        <div class="field"><label for="standalone-note-title">Title</label><input id="standalone-note-title" type="text" placeholder="Idea, decision, or snippet"></div>
        <div class="field"><label for="standalone-note-body">Body</label><textarea id="standalone-note-body" rows="6" placeholder="Write the thought before it disappears"></textarea></div>
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
      ${notes.map(standaloneNoteCard).join("") || `<div class="panel empty-state"><span class="meta">No notes</span><h2>Start with one thought</h2><p>Standalone notes can be linked to bookmarks later through the reader workflow.</p></div>`}
    </section>
  `));
  const form = document.querySelector("#standalone-note-form");
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
  document.querySelectorAll("[data-note-save]").forEach((button) => {
    button.addEventListener("click", () => updateStandaloneNote(button));
  });
  document.querySelectorAll("[data-note-delete]").forEach((button) => {
    button.addEventListener("click", () => deleteStandaloneNote(button));
  });
}

function standaloneNoteCard(note) {
  return `<article class="panel form" data-note="${escapeHTML(note.id)}">
    <div class="field"><label for="note-title-${escapeHTML(note.id)}">Title</label><input id="note-title-${escapeHTML(note.id)}" data-note-title value="${escapeHTML(note.title || "")}"></div>
    <div class="field"><label for="note-body-${escapeHTML(note.id)}">Body</label><textarea id="note-body-${escapeHTML(note.id)}" data-note-body rows="5">${escapeHTML(note.body || "")}</textarea></div>
    <p class="meta">${note.bookmark_id ? `Linked to bookmark ${escapeHTML(note.bookmark_id)}` : "Standalone"} · ${escapeHTML(note.updated_at || "")}</p>
    <p class="button-row">
      ${note.bookmark_id ? `<a class="button secondary" href="/bookmark/${escapeHTML(note.bookmark_id)}">Open bookmark</a>` : ""}
      <button type="button" data-note-save="${escapeHTML(note.id)}">Save changes</button>
      <button type="button" class="danger" data-note-delete="${escapeHTML(note.id)}">Delete</button>
    </p>
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
    render();
  } catch (err) {
    ui.toast(err.message, "error");
  } finally {
    done();
  }
}

async function bookmarkPage() {
  await requireUser();
  const id = location.pathname.split("/").pop();
  const bookmark = await api(`/bookmarks/${id}`);
  const related = await api(`/bookmarks/${id}/related?limit=4`).catch(() => ({ related: [] }));
  const summary = bookmark.ai_summary || {};
  setRoot(shell(bookmark.title || "Bookmark", `
    <article class="panel reader">
      <p class="meta">${bookmark.domain || ""} · ${bookmark.reading_time || 0} min</p>
      <p class="button-row">
        <a class="button" href="${escapeHTML(bookmark.url)}" target="_blank" rel="noreferrer noopener">Open original</a>
        <button type="button" class="secondary" id="toggle-read">${bookmark.read_status ? "Mark unread" : "Mark read"}</button>
        <button type="button" class="secondary" id="review-complete">Review done</button>
        <button type="button" class="danger" id="delete-bookmark">Delete</button>
      </p>
      ${tagList(bookmark.tags || [])}
      ${summaryPanel(summary)}
      <div class="reader-content">${bookmark.html_content || `<p>${escapeHTML(bookmark.text_content || bookmark.description || "No archived text yet.")}</p>`}</div>
    </article>
    <section class="split">
      <form class="panel form" id="annotation-form">
        <h2>Capture a note</h2>
        <div class="field"><label for="annotation-quote">Quote</label><textarea id="annotation-quote" rows="3" placeholder="Paste the passage worth keeping"></textarea></div>
        <div class="field"><label for="annotation-note">Note</label><textarea id="annotation-note" rows="4" placeholder="Your interpretation, decision, or next action"></textarea></div>
        <div class="field"><label for="annotation-tags">Tags</label><input id="annotation-tags" type="text" placeholder="strategy, quote"></div>
        <p class="form-message" id="annotation-message" data-form-message hidden></p>
        <div class="button-row">
          <button type="button" class="secondary" id="use-selection">Use selected text</button>
          <button type="submit">Save annotation</button>
        </div>
      </form>
      <section class="panel">
        <h2>Annotations</h2>
        ${annotationList(bookmark.annotations || [])}
      </section>
    </section>
    <section class="split">
      <form class="panel form" id="note-form">
        <h2>Linked note</h2>
        <div class="field"><label for="note-title">Title</label><input id="note-title" type="text" placeholder="Working note"></div>
        <div class="field"><label for="note-body">Body</label><textarea id="note-body" rows="5" placeholder="Turn this saved item into usable knowledge"></textarea></div>
        <p class="form-message" id="note-message" data-form-message hidden></p>
        <button type="submit">Save note</button>
      </form>
      <section class="panel">
        <h2>Linked notes</h2>
        ${noteList(bookmark.notes || [])}
      </section>
    </section>
    <section class="panel">
      <h2>Related</h2>
      ${relatedList(related.related || [])}
    </section>
  `));
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
          selector: {},
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
  document.querySelectorAll("[data-annotation-delete]").forEach((button) => {
    button.addEventListener("click", () => deleteAnnotation(button));
  });
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
        selector: {},
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
  const confirmed = await ui.confirmDestructive({ title: "Delete annotation", body: "This removes the saved quote and note from this bookmark.", confirm: "Delete", cancel: "Keep annotation" });
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
  await requireUser();
  const section = new URLSearchParams(location.search).get("section") || "profile";
  const tabs = [
    ["profile", "Profile", "Manage your profile and account access."],
    ["import", "Import", "Bring in browser, Pocket, Raindrop, or URL-list exports."],
    ["tags", "Tags", "Normalize your vocabulary and map aliases to canonical tags."],
    ["connections", "Connections", "Connect provider accounts and sync saved items."],
    ["api-keys", "API Keys", "Configure provider keys for enrichment and delivery."],
  ];
  const active = tabs.some(([id]) => id === section) ? section : "profile";
  setRoot(shell("Settings", `<section class="panel tabs" id="settings-tabs">
    <div class="tab-list" role="tablist" aria-label="Settings sections">
      ${tabs.map(([id, label]) => `<button type="button" role="tab" id="tab-${id}" aria-controls="panel-${id}" aria-selected="${id === active}">${label}</button>`).join("")}
    </div>
    ${tabs.map(([id, label, copy]) => `<div role="tabpanel" id="panel-${id}" aria-labelledby="tab-${id}"><h2>${label}</h2><p>${copy}</p>${settingsPanel(id)}</div>`).join("")}
  </section>`));
  ui.tabs(document.querySelector("#settings-tabs"));
  bindProfilePanel();
  bindImportPanel();
  bindTagSettingsPanel();
  bindAPIKeysPanel();
}

function settingsPanel(id) {
  if (id === "profile") return profilePanel();
  if (id === "import") return importPanel();
  if (id === "tags") return tagSettingsPanel();
  if (id === "api-keys") return apiKeysPanel();
  return `<section class="panel"><p class="meta">No connection controls yet.</p></section>`;
}

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

function tagSettingsPanel() {
  return `<section class="split">
    <form class="panel form" id="tag-form">
      <h3>Canonical tag</h3>
      <div class="field"><label for="tag-name">Name</label><input id="tag-name" type="text" placeholder="Research"></div>
      <p class="form-message" id="tag-message" data-form-message hidden></p>
      <button type="submit">Create tag</button>
    </form>
    <form class="panel form" id="tag-alias-form">
      <h3>Alias</h3>
      <div class="field"><label for="alias-tag">Canonical tag</label><select id="alias-tag"></select></div>
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

function apiKeysPanel() {
  return `<section class="split">
    <section class="panel">
      <h3>Status</h3>
      <div id="api-key-status" class="stack"><p class="meta">Loading key status.</p></div>
    </section>
    <form class="panel form" id="api-keys-form">
      <h3>Update keys</h3>
      <div class="field"><label for="gemini-api-key">Gemini API key</label><input id="gemini-api-key" type="password" autocomplete="off" placeholder="Leave blank to keep current value"></div>
      <div class="field"><label for="resend-api-key">Resend API key</label><input id="resend-api-key" type="password" autocomplete="off" placeholder="Leave blank to keep current value"></div>
      <div class="field"><label for="x-client-id">X client ID</label><input id="x-client-id" type="text" autocomplete="off"></div>
      <div class="field"><label for="x-client-secret">X client secret</label><input id="x-client-secret" type="password" autocomplete="off" placeholder="Leave blank to keep current value"></div>
      <div class="field"><label for="x-redirect-uri">X redirect URI</label><input id="x-redirect-uri" type="url" autocomplete="off"></div>
      <p class="form-message" id="api-keys-message" data-form-message hidden></p>
      <button type="submit">Save keys</button>
    </form>
  </section>`;
}

async function bindAPIKeysPanel() {
  const form = document.querySelector("#api-keys-form");
  const status = document.querySelector("#api-key-status");
  if (!form || !status) return;
  const refresh = async () => {
    try {
      const keys = await api("/admin/api-keys");
      status.innerHTML = apiKeyStatus(keys);
      document.querySelector("#x-redirect-uri").value = keys.x_redirect_uri?.value || "";
    } catch (err) {
      status.innerHTML = `<p class="meta">${escapeHTML(err.status === 403 ? "Admin access required." : err.message)}</p>`;
    }
  };
  await refresh();
  form.addEventListener("submit", async (event) => {
    event.preventDefault();
    const done = setButtonBusy(event.submitter, "Saving");
    setFormMessage(form);
    const body = {};
    for (const [key, selector] of Object.entries({
      gemini_api_key: "#gemini-api-key",
      resend_api_key: "#resend-api-key",
      x_client_id: "#x-client-id",
      x_client_secret: "#x-client-secret",
      x_redirect_uri: "#x-redirect-uri",
    })) {
      const value = document.querySelector(selector).value.trim();
      if (value) body[key] = value;
    }
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
  return ["gemini_api_key", "resend_api_key", "x_client_id", "x_client_secret", "x_redirect_uri"]
    .map((key) => {
      const item = keys[key] || {};
      const label = key.replaceAll("_", " ");
      const value = item.masked_value || item.value || "";
      return `<p><strong>${escapeHTML(label)}</strong> <span class="meta">${item.configured || value ? "configured" : "not configured"} · ${escapeHTML(item.source || "none")} ${value ? `· ${escapeHTML(value)}` : ""}</span></p>`;
    })
    .join("");
}

function importPanel() {
  return `<section class="split">
    <form class="panel form" id="import-form">
      <h3>Paste export</h3>
      <div class="field"><label for="import-content">Export content</label><textarea id="import-content" rows="9" placeholder="Paste browser HTML, Pocket/Raindrop/Linkwarden JSON, or one URL per line"></textarea></div>
      <p class="form-message" id="import-message" data-form-message hidden></p>
      <button type="submit">Start import</button>
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
}

function renderImportJobs(jobs) {
  const target = document.querySelector("#import-jobs");
  if (!target) return;
  target.innerHTML = (jobs || []).map((job) => `<article class="annotation">
    <p><strong>${escapeHTML(job.status || "import")}</strong> <span class="meta">${Number(job.total_bookmarks || 0)} items</span></p>
    ${importSourceReport(job.source_report || [])}
    ${importSourceItems(job.items || [])}
    <p class="meta">Fetched ${Number(job.content_fetched || 0)} · AI ${Number(job.ai_processed || 0)} · Failed ${Number(job.failed || 0)}</p>
  </article>`).join("") || `<p class="meta">No imports yet.</p>`;
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
  setRoot(shell("Review", `
    <section class="split">
      ${memoryCard(memory)}
      <section class="panel">
        <span class="meta">Daily review</span>
        <h2>Keep saved pages from becoming a pile</h2>
        <p>Complete what is useful, snooze what needs time, archive what should stop resurfacing.</p>
      </section>
    </section>
    <section class="grid" aria-label="Review queue">
      ${(queue.items || []).map(reviewCard).join("") || `<div class="panel empty-state"><span class="meta">Clear</span><h2>No review items due</h2><p>Arivu will bring older or high-signal saves back when they are ready.</p></div>`}
    </section>
  `));
  document.querySelectorAll("[data-review-complete]").forEach((button) => {
    button.addEventListener("click", () => reviewAction(button, "complete"));
  });
  document.querySelectorAll("[data-review-snooze]").forEach((button) => {
    button.addEventListener("click", () => reviewAction(button, "snooze"));
  });
  document.querySelectorAll("[data-review-archive]").forEach((button) => {
    button.addEventListener("click", () => reviewAction(button, "archive"));
  });
}

function memoryCard(memory) {
  if (!memory.has_memory || !memory.bookmark) {
    return `<section class="panel empty-state"><span class="meta">Daily memory</span><h2>No memory due</h2><p>${escapeHTML(memory.message || "Older, high-signal saves will appear here.")}</p></section>`;
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
      <button type="button" data-review-complete="${escapeHTML(id)}">Done</button>
      <button type="button" class="secondary" data-review-snooze="${escapeHTML(id)}">Snooze</button>
      <button type="button" class="secondary" data-review-archive="${escapeHTML(id)}">Archive</button>
    </p>
  </section>`;
}

function reviewCard(item) {
  const id = `${item.item_type || "bookmark"}:${item.id}`;
  const isNote = item.item_type === "note";
  return `<article class="panel bookmark">
    <span class="meta">${escapeHTML(item.resurfacing_reason || item.domain || item.source || "review")}</span>
    <h2>${escapeHTML(item.title || item.url || "Untitled")}</h2>
    <p>${escapeHTML(item.description || item.ai_summary?.one_sentence || "")}</p>
    <p class="button-row">
      <a class="button secondary" href="${isNote ? "/notes" : `/bookmark/${escapeHTML(item.id)}`}">Open</a>
      <button type="button" data-review-complete="${escapeHTML(id)}">Done</button>
      <button type="button" class="secondary" data-review-snooze="${escapeHTML(id)}">Snooze</button>
      ${isNote ? "" : `<button type="button" class="secondary" data-review-archive="${escapeHTML(id)}">Archive</button>`}
    </p>
  </article>`;
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
      ${groups.map(duplicateGroup).join("") || `<div class="panel empty-state"><span class="meta">Clean</span><h2>No duplicates found</h2><p>Exact URL and high-similarity matches will appear here.</p></div>`}
    </section>
  `));
  document.querySelectorAll("[data-merge]").forEach((button) => {
    button.addEventListener("click", async () => {
      const ids = button.dataset.merge.split(",");
      const confirmed = await ui.confirmDestructive({ title: "Merge duplicates", body: "Arivu will keep the first item and move useful data from the rest.", confirm: "Merge", cancel: "Cancel" });
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
  const query = params.get("query") || "";
  const graph = await api("/knowledge-graph/explore?limit=60");
  const search = query ? await api(`/knowledge-graph/search?query=${encodeURIComponent(query)}&limit=12`).catch(() => ({ results: [] })) : null;
  setRoot(shell("Knowledge graph", `
    <form class="toolbar" role="search" id="graph-search">
      <label class="sr-only" for="graph-query">Search graph</label>
      <input id="graph-query" type="search" placeholder="Search concepts, entities, or meaning" value="${escapeHTML(query)}">
      <button class="secondary" type="submit">Search</button>
    </form>
    <section class="grid">
      <div class="panel"><span class="meta">Bookmarks</span><h2>${graph.total_bookmarks || 0}</h2></div>
      <div class="panel"><span class="meta">Entities</span><h2>${graph.total_entities || 0}</h2></div>
      <div class="panel"><span class="meta">Concepts</span><h2>${graph.total_concepts || 0}</h2></div>
    </section>
    <section class="split">
      <div class="panel"><h2>Concepts</h2>${termCloud(graph.concepts || [])}</div>
      <div class="panel"><h2>Entities</h2>${termCloud(graph.entities || [])}</div>
    </section>
    ${search ? `<section class="panel"><h2>Search results</h2>${relatedList(search.results || [])}</section>` : ""}
    <section class="panel"><h2>Recent graph nodes</h2>${relatedList(graph.bookmarks || [])}</section>
  `));
  document.querySelector("#graph-search").addEventListener("submit", (event) => {
    event.preventDefault();
    const value = document.querySelector("#graph-query").value.trim();
    navigate(`/knowledge-graph${value ? `?query=${encodeURIComponent(value)}` : ""}`);
  });
}

function termCloud(terms) {
  if (!terms.length) return `<p class="meta">Terms appear after enrichment.</p>`;
  return `<div class="chips term-cloud">${terms.slice(0, 30).map((term) => `<span>${escapeHTML(term)}</span>`).join("")}</div>`;
}

async function analyticsPage() {
  await requireUser();
  const [summary, topics, insights] = await Promise.all([
    api("/analytics/summary"),
    api("/analytics/topics").catch(() => ({ topics: [] })),
    api("/analytics/insights").catch(() => ({ insights: [] })),
  ]);
  setRoot(shell("Analytics", `<section class="grid">
    <div class="panel"><span class="meta">Bookmarks</span><h2>${summary.total_bookmarks || 0}</h2></div>
    <div class="panel"><span class="meta">Collections</span><h2>${summary.total_collections || 0}</h2></div>
    <div class="panel"><span class="meta">Unread</span><h2>${summary.unread_bookmarks || 0}</h2></div>
    <div class="panel"><span class="meta">Read</span><h2>${summary.read_bookmarks || 0}</h2></div>
  </section>
  <section class="split">
    <div class="panel"><h2>Top domains</h2>${topicList(topics.topics || [])}</div>
    <div class="panel"><h2>Signals</h2>${insightList(insights.insights || [])}</div>
  </section>`));
}

function topicList(items) {
  if (!items.length) return `<p class="meta">No domain patterns yet.</p>`;
  return `<div class="stack">${items.map((item) => `<p><strong>${escapeHTML(item.topic)}</strong> <span class="meta">${item.count}</span></p>`).join("")}</div>`;
}

function insightList(items) {
  if (!items.length) return `<p class="meta">Insights appear after you save and revisit pages.</p>`;
  return `<div class="stack">${items.map((item) => `<p><span class="meta">${escapeHTML(item.severity || "info")}</span><br>${escapeHTML(item.message || "")}</p>`).join("")}</div>`;
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
    await requireUser();
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
  state.pendingRoutes += 1;
  document.body.classList.add("is-routing");
  const route = routes.find(routeMatches);
  const page = route ? route.page : location.pathname === "/" ? () => navigate(state.user ? "/dashboard" : "/auth", true) : dashboardPage;
  try {
    if (route?.access === "protected") await requireUser();
    await page();
    const actions = document.querySelector("#global-actions");
    if (actions) {
      ui.menu(actions, [
        { label: "Dashboard", action: () => navigate("/dashboard") },
        { label: "Settings", action: () => navigate("/settings") },
        { label: "Admin", action: () => navigate("/admin") },
      ]);
    }
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
addEventListener("popstate", render);
render();
