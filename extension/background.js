const DEFAULT_API_URL = 'https://arivu.app/api';
const MENU_IDS = {
  page: 'arivu-save-page',
  link: 'arivu-save-link',
  selection: 'arivu-save-selection',
};

async function getApiUrl() {
  const result = await chrome.storage.local.get(['apiUrl']);
  return result.apiUrl || DEFAULT_API_URL;
}

async function tokenSenderAllowed(sender) {
  if (!sender.url) return false;

  const allowedOrigins = new Set(['https://arivu.app', 'http://localhost']);

  try {
    const apiUrl = new URL(await getApiUrl());
    allowedOrigins.add(apiUrl.origin);
  } catch {
    // Invalid custom URL should not block the built-in origins.
  }

  try {
    const senderUrl = new URL(sender.url);
    return senderUrl.hostname === 'localhost' || allowedOrigins.has(senderUrl.origin);
  } catch {
    return false;
  }
}

function installContextMenus() {
  chrome.contextMenus.removeAll(() => {
    chrome.contextMenus.create({
      id: MENU_IDS.page,
      title: 'Save page to Arivu',
      contexts: ['page'],
    });
    chrome.contextMenus.create({
      id: MENU_IDS.link,
      title: 'Save link to Arivu',
      contexts: ['link'],
    });
    chrome.contextMenus.create({
      id: MENU_IDS.selection,
      title: 'Save selection to Arivu',
      contexts: ['selection'],
    });
  });
}

function bookmarkPayload(url, selectionText) {
  const payload = { url };
  const text = (selectionText || '').trim();

  if (text) {
    payload.annotation = text.slice(0, 5000);
  }

  return payload;
}

async function saveBookmark(url, selectionText) {
  const apiUrl = await getApiUrl();
  const tokenResult = await chrome.storage.session.get(['accessToken']);

  if (!tokenResult.accessToken) {
    throw new Error('Missing extension token');
  }

  const headers = {
    'Content-Type': 'application/json',
    'Authorization': `Bearer ${tokenResult.accessToken}`,
  };
  const payload = bookmarkPayload(url, selectionText);
  const response = await fetch(`${apiUrl}/extension/bookmarks`, {
    method: 'POST',
    headers,
    body: JSON.stringify(payload),
  });

  if (response.ok) return;

  if (payload.annotation && (response.status === 400 || response.status === 422)) {
    const fallback = await fetch(`${apiUrl}/extension/bookmarks`, {
      method: 'POST',
      headers,
      body: JSON.stringify({ url }),
    });
    if (fallback.ok) return;
  }

  if (response.status === 401) {
    await chrome.storage.session.remove(['accessToken', 'refreshToken']);
  }

  throw new Error(`Save failed: ${response.status}`);
}

async function showResult(tabId, label) {
  if (!tabId) return;

  await chrome.action.setBadgeBackgroundColor({ tabId, color: '#116B4F' });
  await chrome.action.setBadgeText({ tabId, text: label });
  setTimeout(() => chrome.action.setBadgeText({ tabId, text: '' }), 1600);
}

async function saveFromTab(tab, selectionText) {
  if (!tab?.id || !tab.url) return;

  try {
    await saveBookmark(tab.url, selectionText);
    await showResult(tab.id, 'OK');
  } catch (error) {
    console.error('Arivu save failed:', error);
    await showResult(tab.id, '!');
  }
}

chrome.runtime.onInstalled.addListener(installContextMenus);
chrome.runtime.onStartup.addListener(installContextMenus);

chrome.contextMenus.onClicked.addListener((info, tab) => {
  const url = info.linkUrl || info.pageUrl || tab?.url;
  if (!url) return;

  saveFromTab({ id: tab?.id, url }, info.selectionText);
});

chrome.commands.onCommand.addListener(async (command) => {
  if (command !== 'save-bookmark') return;

  const [tab] = await chrome.tabs.query({ active: true, currentWindow: true });
  await saveFromTab(tab);
});

chrome.runtime.onMessage.addListener((request, sender, sendResponse) => {
  if (request.action === 'saveTokens') {
    tokenSenderAllowed(sender).then((allowed) => {
      if (!allowed) {
        sendResponse({ success: false });
        return;
      }

      chrome.storage.session.set({
        accessToken: request.accessToken,
        refreshToken: request.refreshToken,
      });
      sendResponse({ success: true });
    });
  }
  return true;
});
