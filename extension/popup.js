const DEFAULT_API_URL = 'https://arivu.app/api';
const INLINE_ANNOTATION_ORIGINS = ['https://*/*', 'http://*/*'];

let apiUrl = DEFAULT_API_URL;

async function getApiUrl() {
  const result = await chrome.storage.local.get(['apiUrl']);
  return result.apiUrl || DEFAULT_API_URL;
}

async function ensureApiPermission(value) {
  const pattern = ArivuExtensionURL.apiOriginPattern(value);
  if (ArivuExtensionURL.builtInApiOrigin(pattern) || !chrome.permissions?.request) return;

  const allowed = await chrome.permissions.contains({ origins: [pattern] });
  if (allowed) return;

  const granted = await chrome.permissions.request({ origins: [pattern] });
  if (!granted) throw new Error('Permission denied for that Arivu origin');
}

async function configureApiOrigin(value) {
  await chrome.runtime.sendMessage({ action: 'configureApiOrigin', apiUrl: value }).catch(() => {});
}

function showSettingsStatus(message, kind) {
  const target = document.getElementById('settingsStatus');
  if (!target) return;
  target.className = `status ${kind}`;
  target.textContent = message;
  target.style.display = 'block';
}

async function init() {
  apiUrl = await getApiUrl();

  const tokenResult = await chrome.storage.session.get(['accessToken', 'refreshToken']);
  if (!tokenResult.accessToken) {
    document.getElementById('loginPrompt').style.display = 'block';
    const loginLink = document.getElementById('loginLink');
    try {
      loginLink.href = ArivuExtensionURL.appUrl(apiUrl, '/auth');
    } catch {
      // Invalid URL — keep default href from HTML
    }
    return;
  }

  document.getElementById('saveForm').style.display = 'block';

  const [tab] = await chrome.tabs.query({ active: true, currentWindow: true });

  document.getElementById('url').value = tab.url;
  document.getElementById('title').value = tab.title;

  loadCollections();
}

async function loadCollections() {
  try {
    const response = await chrome.runtime.sendMessage({
      action: 'apiRequest',
      request: { path: '/extension/collections' },
    });
    if (!response?.success || !Array.isArray(response.data)) return;
    const collections = response.data;

    const select = document.getElementById('collection');
    collections.forEach(col => {
      const option = document.createElement('option');
      option.value = col.id;
      option.textContent = col.name;
      select.appendChild(option);
    });
  } catch (error) {
    console.error('Failed to load collections:', error);
  }
}

function splitTags(value) {
  return String(value || '')
    .split(',')
    .map(tag => tag.trim())
    .filter(Boolean)
    .slice(0, 20);
}

document.getElementById('bookmarkForm').addEventListener('submit', async (e) => {
  e.preventDefault();

  const btn = document.getElementById('saveBtn');
  const status = document.getElementById('status');

  btn.disabled = true;
  btn.textContent = 'Saving...';
  status.style.display = 'none';

  try {
    const url = document.getElementById('url').value;
    const title = document.getElementById('title').value.trim();
    const collectionId = document.getElementById('collection').value || null;
    const note = document.getElementById('note').value.trim();
    const tags = splitTags(document.getElementById('tags').value);
    const payload = { url, collection_id: collectionId };
    if (title) payload.title = title;
    if (note) payload.note = note;
    if (tags.length) payload.tags = tags;

    const response = await chrome.runtime.sendMessage({
      action: 'apiRequest',
      request: { path: '/extension/bookmarks', method: 'POST', body: payload },
    });

    if (response?.success) {
      const result = response.data;
      const bookmarkId = result?.bookmark?.id;
      status.className = 'status success';
      status.textContent = 'Saved to Inbox';
      status.style.display = 'block';
      const actions = document.getElementById('savedActions');
      actions.replaceChildren(savedLink(ArivuExtensionURL.appUrl(apiUrl, '/inbox'), 'Open Inbox'));
      if (bookmarkId) actions.appendChild(savedLink(ArivuExtensionURL.appUrl(apiUrl, `/bookmark/${encodeURIComponent(bookmarkId)}`), 'Open Item'));
      actions.style.display = 'grid';
    } else if (response?.status === 401) {
      status.className = 'status error';
      status.textContent = 'Session expired — reopen Arivu to reconnect';
      status.style.display = 'block';
    } else {
      throw new Error('Failed to save');
    }
  } catch (error) {
    status.className = 'status error';
    status.textContent = 'Failed to save bookmark';
    status.style.display = 'block';
  } finally {
    btn.disabled = false;
    btn.textContent = 'Save Bookmark';
  }
});

function savedLink(href, label) {
  const link = document.createElement('a');
  link.href = href;
  link.target = '_blank';
  link.rel = 'noreferrer';
  link.textContent = label;
  return link;
}

// Settings toggle
const settingsToggle = document.getElementById('settingsToggle');
const settingsPanel = document.getElementById('settingsPanel');
const apiUrlInput = document.getElementById('apiUrlInput');
const inlineAnnotationsEnabled = document.getElementById('inlineAnnotationsEnabled');

async function loadInlineAnnotationSetting() {
  const settings = await chrome.storage.local.get(['inlineAnnotationsEnabled']);
  inlineAnnotationsEnabled.checked = Boolean(settings.inlineAnnotationsEnabled);
}

async function configureInlineAnnotations(enabled) {
  if (enabled) {
    if (!chrome.permissions?.request) throw new Error('Your browser cannot grant page access for inline annotations');
    const granted = await chrome.permissions.request({ origins: INLINE_ANNOTATION_ORIGINS });
    if (!granted) throw new Error('Permission denied for inline annotations');
  }
  const response = await chrome.runtime.sendMessage({ action: 'configureInlineAnnotations', enabled });
  if (!response?.success) throw new Error('Could not configure inline annotations');
}

settingsToggle.addEventListener('click', async () => {
  const isVisible = settingsPanel.style.display === 'block';
  settingsPanel.style.display = isVisible ? 'none' : 'block';

  if (!isVisible) {
    apiUrlInput.value = await getApiUrl();
    await loadInlineAnnotationSetting();
  }
});

apiUrlInput.addEventListener('change', async () => {
  const value = apiUrlInput.value.trim();
  if (value) {
    try {
      const normalized = ArivuExtensionURL.normalizeApiUrl(value);
      await ensureApiPermission(normalized);
      await chrome.storage.local.set({ apiUrl: normalized });
      await configureApiOrigin(normalized);
      apiUrl = normalized;
      apiUrlInput.value = normalized;
      showSettingsStatus('API URL saved', 'success');
      return;
    } catch (error) {
      showSettingsStatus(error.message || 'Could not use that API URL', 'error');
      return;
    }
  }

  await chrome.storage.local.remove(['apiUrl']);
  apiUrl = DEFAULT_API_URL;
  await configureApiOrigin(DEFAULT_API_URL);
  showSettingsStatus('Using default Arivu API', 'success');
});

inlineAnnotationsEnabled.addEventListener('change', async () => {
  const enabled = inlineAnnotationsEnabled.checked;
  inlineAnnotationsEnabled.disabled = true;
  try {
    await configureInlineAnnotations(enabled);
    showSettingsStatus(enabled ? 'Inline annotations enabled' : 'Inline annotations disabled', 'success');
  } catch (error) {
    inlineAnnotationsEnabled.checked = !enabled;
    showSettingsStatus(error.message || 'Could not configure inline annotations', 'error');
  } finally {
    inlineAnnotationsEnabled.disabled = false;
  }
});

init();
