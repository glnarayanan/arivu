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
  { prefix: "/inbox", page: inboxPage, access: "protected" },
  { prefix: "/assistant", page: assistantPage, access: "protected" },
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
  history[replace ? "replaceState" : "pushState"]({}, "", path);
  render();
}

function shell(title, content) {
  const nav = [
    ["/dashboard", "Bookmarks"],
    ["/inbox", "Inbox"],
    ["/assistant", "Assistant"],
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
      <input id="filter-date-to" type="date" aria-label="Saved before" value="${escapeHTML(params.get("date_to") || "")}">
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
    <div class="stack">${citations.map((item, index) => `<article class="annotation">
      <p><strong>[${index + 1}] ${escapeHTML(item.title || item.url)}</strong> <span class="meta">${escapeHTML(item.type || "bookmark")} · ${escapeHTML(item.domain || "")}</span></p>
      <p>${escapeHTML(item.snippet || "")}</p>
      <a class="text-link" href="${item.type === "note" ? `/notes?note=${encodeURIComponent(item.id)}` : `/bookmark/${escapeHTML(item.id)}`}">Open citation</a>
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
        <span class="meta">Processing loop</span>
        <h2>${Number(counts.inbox || 0)} unprocessed</h2>
        <p>Decide why each saved item matters, move active work into processing, then mark finished items processed before review.</p>
      </section>
      <section class="panel">
        <h2>Stages</h2>
        <div class="chips stage-tabs">
          ${["inbox", "processing", "processed", "archived"].map((name) => `<a class="${name === stage ? "active" : ""}" ${name === stage ? `aria-current="page"` : ""} href="/inbox?stage=${name}">${name} · ${Number(counts[name] || 0)}</a>`).join("")}
        </div>
      </section>
    </section>
    <section class="stack">
      ${items.map(inboxCard).join("") || `<div class="panel empty-state"><span class="meta">Clear</span><h2>No ${escapeHTML(stage)} items</h2><p>New captures and notes appear in the inbox until you process them.</p></div>`}
    </section>
  `));
  document.querySelectorAll("[data-inbox-stage]").forEach((button) => {
    button.addEventListener("click", () => updateInboxItem(button, button.dataset.inboxStage));
  });
  document.querySelectorAll("[data-inbox-save]").forEach((button) => {
    button.addEventListener("click", () => updateInboxItem(button, button.closest("[data-inbox-item]").querySelector("[data-next-stage]").value));
  });
  bindActionItemControls();
  bindPriorityButtons();
}

function inboxCard(item) {
  const itemID = `${item.item_type}:${item.id}`;
  const isNote = item.item_type === "note";
  return `<article class="panel form" data-inbox-item="${escapeHTML(itemID)}">
    <span class="meta">${escapeHTML(item.item_type || "item")} · ${escapeHTML(item.domain || item.source || item.stage || "")}</span>
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
          ${["inbox", "processing", "processed", "archived"].map((stage) => `<option value="${stage}" ${stage === item.stage ? "selected" : ""}>${stage}</option>`).join("")}
        </select>
      </div>
    </div>
    <p class="button-row">
      <a class="button secondary" href="${isNote ? `/notes?note=${encodeURIComponent(item.id)}` : `/bookmark/${escapeHTML(item.id)}`}">Open</a>
      <button type="button" data-inbox-save="${escapeHTML(itemID)}">Save state</button>
      <button type="button" class="secondary" data-inbox-stage="processing">Processing</button>
      <button type="button" class="secondary" data-inbox-stage="processed">Processed</button>
      <button type="button" class="secondary" data-inbox-stage="archived">Archive</button>
    </p>
    ${actionItemsPanel(item.item_type, item.id, item.action_items || [])}
  </article>`;
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

async function assistantPage() {
  await requireUser();
  const params = new URLSearchParams(location.search);
  const status = params.get("status") || "pending";
  const result = await api(`/assistant/actions?status=${encodeURIComponent(status)}`);
  const actions = result.actions || [];
  setRoot(shell("Assistant actions", `
    <section class="split">
      <section class="panel">
        <span class="meta">Approval ledger</span>
        <h2>${actions.length} ${escapeHTML(status)} proposals</h2>
        <p>Assistant suggestions stay inert until you approve them. Each approval rechecks ownership and executes one bounded mutation.</p>
      </section>
      <form class="panel form" id="assistant-action-form">
        <h2>Propose action</h2>
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
    </section>
    <section class="panel">
      <h2>Queue</h2>
      <div class="chips stage-tabs">
        ${["pending", "executed", "failed", "rejected", "all"].map((name) => `<a class="${name === status ? "active" : ""}" ${name === status ? `aria-current="page"` : ""} href="/assistant?status=${name}">${name}</a>`).join("")}
      </div>
    </section>
    <section class="stack">
      ${actions.map(assistantActionCard).join("") || `<div class="panel empty-state"><span class="meta">No proposals</span><h2>Nothing waiting</h2><p>Approved assistant work appears here as a ledger.</p></div>`}
    </section>
  `));
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
    <div class="split compact-split">
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
      ${isPending ? `<button type="button" data-assistant-approve="${actionID}">Approve</button><button type="button" class="secondary" data-assistant-reject="${actionID}">Reject</button>` : ""}
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
  const done = setButtonBusy(button, decision === "approve" ? "Approving" : "Rejecting");
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
  focusNoteFromQuery();
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

function focusNoteFromQuery() {
  const targetID = new URLSearchParams(location.search).get("note");
  if (!targetID) return;
  const target = Array.from(document.querySelectorAll("[data-note]")).find((item) => item.dataset.note === targetID);
  if (!target) return;
  target.tabIndex = -1;
  target.focus({ preventScroll: true });
  target.scrollIntoView({ block: "center" });
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
  const [bookmark, related, notesResult] = await Promise.all([
    api(`/bookmarks/${id}`),
    api(`/bookmarks/${id}/related?limit=4`).catch(() => ({ related: [] })),
    api("/notes").catch(() => ({ notes: [] })),
  ]);
  const summary = bookmark.ai_summary || {};
  const itemState = bookmark.item_state || { stage: "inbox", importance: 0, next_action: "" };
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
      ${processingStrip(id, itemState)}
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
    <section class="split">
      <form class="panel form" id="link-form">
        <h2>Link note</h2>
        <div class="field"><label for="link-note">Note</label><select id="link-note">${noteOptions(notesResult.notes || [], bookmark.notes || [])}</select></div>
        <div class="field"><label for="link-label">Label</label><input id="link-label" type="text" maxlength="80" placeholder="supports, contradicts, next step"></div>
        <p class="form-message" id="link-message" data-form-message hidden></p>
        <button type="submit">Create link</button>
      </form>
      <section class="panel">
        <h2>Linked graph</h2>
        ${linkList(bookmark.links || {})}
      </section>
    </section>
    <section class="split">
      <form class="panel form" data-action-item-form data-item-type="bookmark" data-item-id="${escapeHTML(id)}">
        <h2>Action item</h2>
        <div class="field"><label for="action-item-title">Task</label><input id="action-item-title" data-action-item-title type="text" maxlength="300" placeholder="Concrete thing to do with this"></div>
        <p class="form-message" data-form-message hidden></p>
        <button type="submit">Add task</button>
      </form>
      <section class="panel">
        <h2>Action items</h2>
        ${actionItemsList(bookmark.action_items || [])}
      </section>
    </section>
    <section class="split">
      <form class="panel form" id="reminder-form">
        <h2>Reminder</h2>
        <div class="field"><label for="reminder-due">Due</label><input id="reminder-due" type="datetime-local" required></div>
        <div class="field"><label for="reminder-note">Note</label><input id="reminder-note" type="text" maxlength="500" placeholder="Why this should come back"></div>
        <p class="form-message" id="reminder-message" data-form-message hidden></p>
        <button type="submit">Set reminder</button>
      </form>
      <section class="panel">
        <h2>Reminders</h2>
        ${reminderList(bookmark.reminders || [])}
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
  document.querySelectorAll("[data-reader-stage]").forEach((button) => {
    button.addEventListener("click", () => updateReaderState(button, button.dataset.readerStage));
  });
  document.querySelector("#processing-save").addEventListener("click", (event) => updateReaderState(event.currentTarget, document.querySelector("#processing-stage").value));
  bindPriorityButtons();
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
  document.querySelectorAll("[data-link-delete]").forEach((button) => {
    button.addEventListener("click", () => deleteLink(button));
  });
  bindActionItemControls();
  const reminderForm = document.querySelector("#reminder-form");
  reminderForm.addEventListener("submit", async (event) => {
    event.preventDefault();
    const dueAt = localDateTimeToRFC3339(document.querySelector("#reminder-due").value);
    if (!dueAt) {
      setFormMessage(reminderForm, "Choose a valid reminder time.");
      return;
    }
    const done = setButtonBusy(event.submitter, "Saving reminder");
    setFormMessage(reminderForm);
    try {
      await api("/reminders", {
        method: "POST",
        body: JSON.stringify({
          item_type: "bookmark",
          item_id: id,
          due_at: dueAt,
          note: document.querySelector("#reminder-note").value,
        }),
      });
      ui.toast("Reminder set", "success");
      render();
    } catch (err) {
      setFormMessage(reminderForm, err.message);
      ui.toast(err.message, "error");
    } finally {
      done();
    }
  });
  document.querySelectorAll("[data-reminder-complete]").forEach((button) => {
    button.addEventListener("click", () => completeReminder(button));
  });
  document.querySelectorAll("[data-reminder-delete]").forEach((button) => {
    button.addEventListener("click", () => deleteReminder(button));
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

function reminderList(reminders) {
  if (!reminders.length) return `<p class="meta">No reminders set.</p>`;
  return `<div class="stack">${reminders.map((reminder) => `<article class="annotation">
    <p><strong>${escapeHTML(formatDate(reminder.due_at))}</strong> <span class="meta">${escapeHTML(reminder.status || "pending")}</span></p>
    ${reminder.note ? `<p>${escapeHTML(reminder.note)}</p>` : ""}
    <p class="button-row">
      ${reminder.status === "completed" ? "" : `<button type="button" data-reminder-complete="${escapeHTML(reminder.id)}">Done</button>`}
      <button type="button" class="danger" data-reminder-delete="${escapeHTML(reminder.id)}">Delete</button>
    </p>
  </article>`).join("")}</div>`;
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
      ${item.status === "completed" ? "" : `<button type="button" data-action-item-complete="${escapeHTML(item.id)}">Done</button>`}
      <button type="button" class="danger" data-action-item-delete="${escapeHTML(item.id)}">Delete</button>
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
  const href = targetType === "note" ? `/notes?note=${encodeURIComponent(targetID)}` : `/bookmark/${escapeHTML(targetID)}`;
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

function processingStrip(bookmarkID, itemState) {
  return `<section class="insight-strip processing-strip" data-reader-item="bookmark:${escapeHTML(bookmarkID)}">
    <span class="meta">Processing · ${escapeHTML(itemState.stage || "inbox")}</span>
    <div class="split compact-split">
      <div class="field">
        <label for="processing-next-action">Next action</label>
        <input id="processing-next-action" data-next-action value="${escapeHTML(itemState.next_action || "")}" maxlength="500" placeholder="What should this item become?">
      </div>
      <fieldset class="field priority-field">
        <legend>Priority</legend>
        <input data-importance type="hidden" value="${Number(itemState.importance || 0)}">
        <div class="priority-buttons">${priorityButtons(itemState.importance || 0)}</div>
      </fieldset>
      <div class="field">
        <label for="processing-stage">Stage</label>
        <select id="processing-stage" data-next-stage>
          ${["inbox", "processing", "processed", "archived"].map((stage) => `<option value="${stage}" ${stage === itemState.stage ? "selected" : ""}>${stage}</option>`).join("")}
        </select>
      </div>
    </div>
    <p class="button-row">
      <button type="button" id="processing-save">Save state</button>
      <button type="button" class="secondary" data-reader-stage="processed">Processed</button>
      <button type="button" class="secondary" data-reader-stage="archived">Archive</button>
    </p>
  </section>`;
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
  bindConnectionsPanel();
  bindAPIKeysPanel();
}

function settingsPanel(id) {
  if (id === "profile") return profilePanel();
  if (id === "import") return importPanel();
  if (id === "tags") return tagSettingsPanel();
  if (id === "connections") return connectionsPanel();
  if (id === "api-keys") return apiKeysPanel();
  return "";
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
  const refresh = async () => {
    try {
      const enabled = await api("/auth/x/enabled");
      if (!enabled.enabled) {
        status.innerHTML = `<p class="meta">X integration is not enabled on this server.</p>`;
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
      ui.toast(`Synced ${Number(result.new_bookmarks || 0)} new bookmarks`, "success");
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
}

function xConnectionStatus(status) {
  if (!status.connected) return `<p class="meta">No X account connected.</p>`;
  return `<p><strong>@${escapeHTML(status.x_username || "x")}</strong> <span class="meta">${escapeHTML(status.x_name || "")}</span></p>
    <p class="meta">Status: ${escapeHTML(status.sync_status || "idle")} · Synced: ${Number(status.total_synced || 0)}${status.last_sync_at ? ` · Last sync: ${escapeHTML(status.last_sync_at)}` : ""}</p>`;
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
      <div class="field"><label for="resend-from-email">Resend from email</label><input id="resend-from-email" type="email" autocomplete="off"></div>
      <div class="field"><label for="x-client-id">X client ID</label><input id="x-client-id" type="text" autocomplete="off"></div>
      <div class="field"><label for="x-client-secret">X client secret</label><input id="x-client-secret" type="password" autocomplete="off" placeholder="Leave blank to keep current value"></div>
      <div class="field"><label for="x-redirect-uri">X redirect URI</label><input id="x-redirect-uri" type="url" autocomplete="off"></div>
      <label class="checkbox-row"><input id="x-integration-enabled" type="checkbox"> X integration enabled</label>
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
    const body = {};
    for (const [key, selector] of Object.entries({
      gemini_api_key: "#gemini-api-key",
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
    gemini_api_key: "Gemini API key",
    resend_api_key: "Resend API key",
    resend_from_email: "Resend from email",
    x_client_id: "X client ID",
    x_client_secret: "X client secret",
    x_redirect_uri: "X redirect URI",
    x_integration_enabled: "X enabled",
  };
  return ["gemini_api_key", "resend_api_key", "resend_from_email", "x_client_id", "x_client_secret", "x_redirect_uri", "x_integration_enabled"]
    .map((key) => {
      const item = keys[key] || {};
      const value = item.masked_value || (item.value === undefined || item.value === null ? "" : String(item.value));
      const configured = item.configured || value;
      return `<article class="annotation">
        <p><strong>${escapeHTML(labels[key])}</strong> <span class="meta">${configured ? "configured" : "not configured"} · ${escapeHTML(item.source || "unset")}${value ? ` · ${escapeHTML(value)}` : ""}</span></p>
        ${item.source === "database" ? `<p class="button-row"><button type="button" class="secondary" data-api-key-revert="${escapeHTML(key)}">Remove override</button></p>` : ""}
      </article>`;
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
  const itemState = item.item_state || {};
  const nextAction = itemState.next_action || "";
  const importance = Number(itemState.importance || 0);
  return `<article class="panel bookmark">
    <span class="meta">${escapeHTML(item.resurfacing_reason || item.domain || item.source || "review")}</span>
    <h2>${escapeHTML(item.title || item.url || "Untitled")}</h2>
    <p>${escapeHTML(item.description || item.ai_summary?.one_sentence || "")}</p>
    ${nextAction || importance ? `<p class="meta">${nextAction ? `Next: ${escapeHTML(nextAction)}` : ""}${nextAction && importance ? " · " : ""}${importance ? `Priority ${importance}` : ""}</p>` : ""}
    <p class="button-row">
      <a class="button secondary" href="${isNote ? `/notes?note=${encodeURIComponent(item.id)}` : `/bookmark/${escapeHTML(item.id)}`}">Open</a>
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
  const params = new URLSearchParams(location.search);
  const active = params.get("section") || "overview";
  const sort = params.get("sort") || "created_at";
  const order = params.get("order") || "desc";
  const [overview, usage, users, system, activity, collections, audit] = await Promise.all([
    api("/admin/overview"),
    api("/admin/api-usage"),
    api(`/admin/users?sort=${encodeURIComponent(sort)}&order=${encodeURIComponent(order)}`),
    api("/admin/system"),
    api("/admin/activity"),
    api("/admin/collections-stats"),
    api("/admin/audit-events?limit=12").catch(() => ({ events: [] })),
  ]);
  const tabs = [
    ["overview", "Overview"],
    ["api", "API Usage"],
    ["users", "Users"],
    ["system", "System"],
    ["activity", "Activity"],
    ["collections", "Collections"],
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
    <div role="tabpanel" id="panel-audit" aria-labelledby="tab-audit"><section class="stack">${auditEvents(audit.events || [])}</section></div>
  </section>`));
  ui.tabs(document.querySelector("#admin-tabs"));
  bindAdminUsersPanel();
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
  const ops = data.provider_usage?.gemini || {};
  return `<section class="grid compact-grid">
    ${adminStat("Gemini calls", data.requests_today, data.gemini_configured ? "Configured" : "Not configured")}
    ${adminStat("Gemini errors", data.provider_usage?.errors_total || 0, data.provider_usage?.since || "")}
    ${adminStat("Summaries done", data.summaries_completed, `Pending ${formatCount(data.summaries_pending)} · Failed ${formatCount(data.summaries_failed)}`)}
    ${adminStat("Jobs queued", data.background_jobs_queued, `Running ${formatCount(data.background_jobs_running)} · Failed ${formatCount(data.background_jobs_failed)}`)}
  </section>
  <section class="stack">${Object.entries(ops).map(([name, item]) => `<article class="annotation">
    <p><strong>${escapeHTML(name)}</strong> <span class="meta">${formatCount(item.requests)} calls · ${formatCount(item.errors)} errors</span></p>
    ${item.last_error ? `<p class="meta">${escapeHTML(item.last_error)}</p>` : ""}
  </article>`).join("") || `<p class="meta">No Gemini calls recorded for this process.</p>`}</section>`;
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
      <button type="button" class="secondary" data-admin-user-detail="${escapeHTML(user.id)}">View</button>
      <button type="button" class="secondary" data-admin-user-action="${action}" data-user-id="${escapeHTML(user.id)}">${action === "ban" ? "Ban" : "Unban"}</button>
      <button type="button" class="secondary" data-admin-user-action="reset-password" data-user-id="${escapeHTML(user.id)}">Reset</button>
      <button type="button" class="danger" data-admin-user-action="delete" data-user-id="${escapeHTML(user.id)}">Delete</button>
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
    const ok = await ui.confirmDestructive({ title: "Delete user", body: "This removes the user and their data.", confirm: "Delete", cancel: "Cancel" });
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
      { label: "Cancel", value: false, kind: "secondary" },
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
