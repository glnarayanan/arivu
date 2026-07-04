const DEFAULT_API_URL = 'https://arivu.app/api';

let currentTab = null;
let accessToken = null;
let refreshToken = null;
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
  accessToken = tokenResult.accessToken;
  refreshToken = tokenResult.refreshToken;

  if (!accessToken) {
    document.getElementById('loginPrompt').style.display = 'block';
    const loginLink = document.getElementById('loginLink');
    const baseUrl = apiUrl.replace('/api', '');
    try {
      const parsed = new URL(baseUrl);
      if (parsed.protocol === 'https:' || parsed.protocol === 'http:') {
        loginLink.href = `${baseUrl}/auth`;
      }
    } catch {
      // Invalid URL — keep default href from HTML
    }
    return;
  }

  document.getElementById('saveForm').style.display = 'block';

  const [tab] = await chrome.tabs.query({ active: true, currentWindow: true });
  currentTab = tab;

  document.getElementById('url').value = tab.url;
  document.getElementById('title').value = tab.title;

  loadCollections();
}

async function loadCollections() {
  try {
    const response = await fetch(`${apiUrl}/extension/collections`, {
      headers: { 'Authorization': `Bearer ${accessToken}` }
    });
    if (!response.ok) return;
    const collections = await response.json();

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
    const collectionId = document.getElementById('collection').value || null;
    const note = document.getElementById('note').value.trim();
    const tags = splitTags(document.getElementById('tags').value);
    const payload = { url, collection_id: collectionId };
    if (note) payload.note = note;
    if (tags.length) payload.tags = tags;

    const response = await fetch(`${apiUrl}/extension/bookmarks`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'Authorization': `Bearer ${accessToken}`
      },
      body: JSON.stringify(payload)
    });

    if (response.ok) {
      status.className = 'status success';
      status.textContent = 'Saved — AI is processing';
      status.style.display = 'block';
      setTimeout(() => window.close(), 1500);
    } else if (response.status === 401) {
      await chrome.storage.session.remove(['accessToken', 'refreshToken']);
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

// Settings toggle
const settingsToggle = document.getElementById('settingsToggle');
const settingsPanel = document.getElementById('settingsPanel');
const apiUrlInput = document.getElementById('apiUrlInput');

settingsToggle.addEventListener('click', async () => {
  const isVisible = settingsPanel.style.display === 'block';
  settingsPanel.style.display = isVisible ? 'none' : 'block';

  if (!isVisible) {
    apiUrlInput.value = await getApiUrl();
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

init();
