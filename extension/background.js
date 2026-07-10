importScripts('url-utils.js');

const DEFAULT_API_URL = 'https://arivu.app/api';
const MENU_IDS = {
  page: 'arivu-save-page',
  link: 'arivu-save-link',
  selection: 'arivu-save-selection',
};
const INLINE_ANNOTATION_SCRIPT_ID = 'arivu-inline-annotation';
const INLINE_ANNOTATION_ORIGINS = ['https://*/*', 'http://*/*'];

async function getApiUrl() {
  const result = await chrome.storage.local.get(['apiUrl']);
  return result.apiUrl || DEFAULT_API_URL;
}

async function registerCustomApiContentScript(apiUrl) {
  if (!chrome.scripting?.registerContentScripts) return;

  await chrome.scripting.unregisterContentScripts({ ids: ['arivu-custom-api'] }).catch(() => {});

  let pattern;
  try {
    pattern = ArivuExtensionURL.apiOriginPattern(apiUrl);
  } catch {
    return;
  }

  if (pattern === 'https://arivu.app/*' || pattern === 'http://localhost/*') return;

  const allowed = await chrome.permissions.contains({ origins: [pattern] });
  if (!allowed) return;

  await chrome.scripting.registerContentScripts([{
    id: 'arivu-custom-api',
    matches: [pattern],
    js: ['content.js'],
    runAt: 'document_idle',
  }]).catch(() => {});
}

async function syncInlineAnnotationOverlay(enabled) {
  if (!chrome.scripting?.registerContentScripts) return false;

  await chrome.scripting.unregisterContentScripts({ ids: [INLINE_ANNOTATION_SCRIPT_ID] }).catch(() => {});
  if (!enabled) return true;

  const allowed = await chrome.permissions?.contains?.({ origins: INLINE_ANNOTATION_ORIGINS });
  if (!allowed) return false;

  await chrome.scripting.registerContentScripts([{
    id: INLINE_ANNOTATION_SCRIPT_ID,
    matches: INLINE_ANNOTATION_ORIGINS,
    js: ['selection-overlay.js'],
    runAt: 'document_idle',
  }]);
  return true;
}

async function configureInlineAnnotations(enabled) {
  const configured = await syncInlineAnnotationOverlay(Boolean(enabled));
  await chrome.storage.local.set({ inlineAnnotationsEnabled: Boolean(enabled) && configured });
  return configured;
}

async function tokenSenderAllowed(sender) {
  if (!sender.url) return false;

  return ArivuExtensionURL.senderOriginAllowed(sender.url, await getApiUrl());
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

async function saveBookmark(url) {
  const apiUrl = await getApiUrl();
  const tokenResult = await chrome.storage.session.get(['accessToken']);

  if (!tokenResult.accessToken) {
    throw new Error('Missing extension token');
  }

  const headers = {
    'Content-Type': 'application/json',
    'Authorization': `Bearer ${tokenResult.accessToken}`,
  };
  const response = await fetch(`${apiUrl}/extension/bookmarks`, {
    method: 'POST',
    headers,
    body: JSON.stringify({ url }),
  });

  if (response.ok) return response.json().catch(() => ({}));

  if (response.status === 401) {
    await chrome.storage.session.remove(['accessToken', 'refreshToken']);
  }

  throw new Error(`Save failed: ${response.status}`);
}

async function saveAnnotation({ url, title = '', quote, note = '' }) {
  const apiUrl = await getApiUrl();
  const tokenResult = await chrome.storage.session.get(['accessToken']);
  const selectedQuote = String(quote || '').replace(/\s+/g, ' ').trim().slice(0, 4000);

  if (!tokenResult.accessToken) throw new Error('Open Arivu to reconnect the extension');
  if (!url || !selectedQuote) throw new Error('Select a passage to annotate');

  const response = await fetch(`${apiUrl}/extension/annotations`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      'Authorization': `Bearer ${tokenResult.accessToken}`,
    },
    body: JSON.stringify({ url, title, quote: selectedQuote, note: String(note || '').trim() }),
  });

  if (response.ok) return response.json();
  if (response.status === 401) {
    await chrome.storage.session.remove(['accessToken', 'refreshToken']);
    throw new Error('Session expired — open Arivu to reconnect the extension');
  }
  const body = await response.json().catch(() => ({}));
  throw new Error(body.detail || `Annotation failed: ${response.status}`);
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
    if ((selectionText || '').trim()) {
      await saveAnnotation({ url: tab.url, title: tab.title || '', quote: selectionText });
    } else {
      await saveBookmark(tab.url);
    }
    await showResult(tab.id, 'OK');
  } catch (error) {
    console.error('Arivu save failed:', error);
    await showResult(tab.id, '!');
  }
}

chrome.runtime.onInstalled.addListener(installContextMenus);
chrome.runtime.onInstalled.addListener(() => {
  getApiUrl().then(registerCustomApiContentScript);
  chrome.storage.local.get(['inlineAnnotationsEnabled']).then((settings) => syncInlineAnnotationOverlay(Boolean(settings.inlineAnnotationsEnabled)));
});
chrome.runtime.onStartup.addListener(() => {
  installContextMenus();
  getApiUrl().then(registerCustomApiContentScript);
  chrome.storage.local.get(['inlineAnnotationsEnabled']).then((settings) => syncInlineAnnotationOverlay(Boolean(settings.inlineAnnotationsEnabled)));
});

chrome.contextMenus.onClicked.addListener(async (info, tab) => {
  const url = info.menuItemId === MENU_IDS.selection
    ? info.pageUrl || tab?.url
    : info.linkUrl || info.pageUrl || tab?.url;
  if (!url) return;

  await saveFromTab({ id: tab?.id, url, title: tab?.title || '' }, info.selectionText);
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
  } else if (request.action === 'configureApiOrigin') {
    registerCustomApiContentScript(request.apiUrl).then(() => {
      sendResponse({ success: true });
    }).catch(() => {
      sendResponse({ success: false });
    });
  } else if (request.action === 'configureInlineAnnotations') {
    configureInlineAnnotations(request.enabled).then((enabled) => {
      sendResponse({ success: enabled });
    }).catch(() => {
      sendResponse({ success: false });
    });
  } else if (request.action === 'captureAnnotation') {
    if (!sender.tab?.id) {
      sendResponse({ success: false, error: 'Annotation requests must come from a browser tab' });
      return false;
    }
    saveAnnotation(request).then(async (result) => {
      await showResult(sender.tab.id, 'OK');
      sendResponse({ success: true, result });
    }).catch(async (error) => {
      await showResult(sender.tab.id, '!');
      sendResponse({ success: false, error: error.message || 'Could not save annotation' });
    });
  }
  return true;
});
