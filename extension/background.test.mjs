import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

function storageGet(values, keys) {
  if (Array.isArray(keys)) return Object.fromEntries(keys.filter((key) => key in values).map((key) => [key, values[key]]));
  if (typeof keys === 'string') return keys in values ? { [keys]: values[keys] } : {};
  return { ...values };
}

function runBackground({ fetchStatus = 200, permissionGranted = true, sessionSet, apiUrl = 'https://arivu.example/api' } = {}) {
  const fetches = [];
  const removedSessionKeys = [];
  const registeredScripts = [];
  const unregisteredScriptIDs = [];
  const local = { apiUrl };
  const session = { accessToken: 'extension-token' };
  const handlers = {};
  const context = {
    chrome: {
      storage: {
        local: {
          get: async (keys) => storageGet(local, keys),
          set: async (values) => Object.assign(local, values),
        },
        session: {
          get: async (keys) => storageGet(session, keys),
          set: sessionSet
            ? (values) => sessionSet(values, session)
            : async (values) => Object.assign(session, values),
          remove: async (keys) => {
            removedSessionKeys.push(...keys);
            keys.forEach((key) => delete session[key]);
          },
        },
      },
      scripting: {
        registerContentScripts: async (scripts) => registeredScripts.push(...scripts),
        unregisterContentScripts: async ({ ids }) => unregisteredScriptIDs.push(...ids),
      },
      permissions: { contains: async () => permissionGranted },
      contextMenus: { removeAll: () => {}, create: () => {}, onClicked: { addListener: (handler) => { handlers.contextMenu = handler; } } },
      action: { setBadgeBackgroundColor: async () => {}, setBadgeText: async () => {} },
      runtime: {
        id: 'extension-id',
        getURL: (path) => `chrome-extension://extension-id/${path}`,
        onInstalled: { addListener: () => {} },
        onStartup: { addListener: () => {} },
        onMessage: { addListener: (handler) => { handlers.message = handler; } },
      },
      commands: { onCommand: { addListener: () => {} } },
    },
    fetch: async (url, options) => {
      fetches.push({ url, options });
      return {
        ok: fetchStatus >= 200 && fetchStatus < 300,
        status: fetchStatus,
        json: async () => ({ annotation: { id: 'annotation-1' } }),
        text: async () => JSON.stringify({ detail: 'Session expired' }),
      };
    },
    URL,
    importScripts: () => {
      vm.runInContext(readFileSync(new URL('./url-utils.js', import.meta.url), 'utf8'), context);
    },
    setTimeout: () => 0,
    console,
  };
  vm.createContext(context);
  vm.runInContext(readFileSync(new URL('./background.js', import.meta.url), 'utf8'), context);
  return { context, fetches, handlers, local, session, registeredScripts, unregisteredScriptIDs, removedSessionKeys };
}

const directCapture = runBackground();
assert.equal(typeof directCapture.context.saveAnnotation, 'function');
await directCapture.context.saveAnnotation({
  url: 'https://example.com/article',
  title: 'Article title',
  quote: 'Selected passage',
  note: 'Why it matters',
});
assert.equal(directCapture.fetches[0].url, 'https://arivu.example/api/extension/annotations');
assert.equal(directCapture.fetches[0].options.headers.Authorization, 'Bearer extension-token');
assert.deepEqual(JSON.parse(directCapture.fetches[0].options.body), {
  url: 'https://example.com/article',
  title: 'Article title',
  quote: 'Selected passage',
  note: 'Why it matters',
});

const pageSave = runBackground();
assert.deepEqual(await pageSave.context.saveBookmark('https://example.com/page'), { annotation: { id: 'annotation-1' } });
assert.equal(pageSave.fetches[0].url, 'https://arivu.example/api/extension/bookmarks');
assert.deepEqual(JSON.parse(pageSave.fetches[0].options.body), { url: 'https://example.com/page' });

const localhostRegistration = runBackground({ apiUrl: 'http://localhost:8080/api' });
await localhostRegistration.context.registerCustomApiContentScript('http://localhost:8080/api');
assert.deepEqual(localhostRegistration.registeredScripts, []);

const messageRequest = runBackground();
const messageResponse = await new Promise((resolve) => {
  assert.equal(messageRequest.handlers.message({
    action: 'apiRequest',
    request: { path: '/extension/collections' },
  }, { id: 'extension-id', url: 'chrome-extension://extension-id/popup.html' }, resolve), true);
});
assert.equal(messageResponse.success, true);
assert.equal(messageResponse.status, 200);
assert.equal(messageRequest.fetches[0].url, 'https://arivu.example/api/extension/collections');
assert.equal(messageRequest.fetches[0].options.headers.Authorization, 'Bearer extension-token');

const expiredMessage = runBackground({ fetchStatus: 401 });
const expiredResponse = await new Promise((resolve) => {
  expiredMessage.handlers.message({ action: 'apiRequest', request: { path: '/extension/collections' } }, { id: 'extension-id', url: 'chrome-extension://extension-id/popup.html' }, resolve);
});
assert.equal(expiredResponse.success, false);
assert.equal(expiredResponse.status, 401);
assert.deepEqual(expiredMessage.removedSessionKeys, ['accessToken', 'refreshToken']);

const rejectedPath = runBackground();
const rejectedResponse = await new Promise((resolve) => {
  rejectedPath.handlers.message({ action: 'apiRequest', request: { path: '/admin/users' } }, { id: 'extension-id', url: 'chrome-extension://extension-id/popup.html' }, resolve);
});
assert.equal(rejectedResponse.success, false);
assert.match(rejectedResponse.error, /not allowed/);
assert.equal(rejectedPath.fetches.length, 0);

const rejectedSender = runBackground();
const rejectedSenderResponse = await new Promise((resolve) => {
  rejectedSender.handlers.message({ action: 'apiRequest', request: { path: '/extension/collections' } }, { id: 'extension-id', url: 'https://example.com', tab: { id: 1 } }, resolve);
});
assert.equal(rejectedSenderResponse.success, false);
assert.equal(rejectedSender.fetches.length, 0);

async function sendTokenMessage(instance, request, sender) {
  return new Promise((resolve) => instance.handlers.message(request, sender, resolve));
}

const exactOriginSender = { id: 'extension-id', tab: { id: 7 }, frameId: 0, url: 'https://arivu.example/auth' };
const bootstrap = runBackground();
const bootstrapResponse = await sendTokenMessage(bootstrap, { action: 'tokenBootstrapContext' }, exactOriginSender);
assert.equal(bootstrapResponse.success, true);
assert.equal(bootstrapResponse.apiUrl, 'https://arivu.example/api');
const saveTokenResponse = await sendTokenMessage(bootstrap, {
  action: 'saveTokens', accessToken: 'new-access', refreshToken: 'new-refresh',
}, exactOriginSender);
assert.equal(saveTokenResponse.success, true);
assert.equal(bootstrap.session.accessToken, 'new-access');

let completeDelayedStorage;
const delayedStorage = runBackground({
  sessionSet: (values, session) => new Promise((resolve) => {
    completeDelayedStorage = () => {
      Object.assign(session, values);
      resolve();
    };
  }),
});
let delayedResponse;
assert.equal(delayedStorage.handlers.message({
  action: 'saveTokens', accessToken: 'delayed-access', refreshToken: 'delayed-refresh',
}, exactOriginSender, (response) => { delayedResponse = response; }), true);
await new Promise((resolve) => setTimeout(resolve, 0));
assert.equal(delayedResponse, undefined);
assert.equal(delayedStorage.session.accessToken, 'extension-token');
completeDelayedStorage();
await new Promise((resolve) => setTimeout(resolve, 0));
assert.equal(delayedResponse.success, true);
assert.equal(delayedStorage.session.accessToken, 'delayed-access');

const rejectedStorage = runBackground({
  sessionSet: async () => { throw new Error('session storage unavailable'); },
});
const rejectedStorageResponse = await sendTokenMessage(rejectedStorage, {
  action: 'saveTokens', accessToken: 'unsaved-access', refreshToken: 'unsaved-refresh',
}, exactOriginSender);
assert.equal(rejectedStorageResponse.success, false);
assert.equal(rejectedStorage.session.accessToken, 'extension-token');

for (const sender of [
  { ...exactOriginSender, id: 'spoofed-extension' },
  { ...exactOriginSender, tab: undefined },
  { ...exactOriginSender, frameId: 2 },
  { ...exactOriginSender, url: 'https://unrelated.example/auth' },
  { ...exactOriginSender, url: 'https://arivu.example:8443/auth' },
]) {
  const rejected = runBackground();
  assert.equal((await sendTokenMessage(rejected, { action: 'tokenBootstrapContext' }, sender)).success, false);
  assert.equal((await sendTokenMessage(rejected, {
    action: 'saveTokens', accessToken: 'spoofed', refreshToken: 'spoofed',
  }, sender)).success, false);
  assert.equal(rejected.session.accessToken, 'extension-token');
}

const selectionMenu = runBackground();
await selectionMenu.context.saveFromTab({ id: 1, url: 'https://example.com/menu', title: 'Menu capture' }, 'Selected from the menu');
assert.equal(selectionMenu.fetches[0].url, 'https://arivu.example/api/extension/annotations');
assert.equal(JSON.parse(selectionMenu.fetches[0].options.body).quote, 'Selected from the menu');

const linkedSelectionMenu = runBackground();
await linkedSelectionMenu.handlers.contextMenu({
  menuItemId: 'arivu-save-selection',
  linkUrl: 'https://example.com/target',
  pageUrl: 'https://example.com/source',
  selectionText: 'Selected link label',
}, { id: 2, title: 'Source page' });
assert.equal(JSON.parse(linkedSelectionMenu.fetches[0].options.body).url, 'https://example.com/source');

const overlayEnabled = runBackground();
assert.equal(await overlayEnabled.context.configureInlineAnnotations(true), true);
assert.equal(overlayEnabled.local.inlineAnnotationsEnabled, true);
assert.equal(overlayEnabled.registeredScripts[0].id, 'arivu-inline-annotation');
assert.deepEqual(Array.from(overlayEnabled.registeredScripts[0].matches), ['https://*/*', 'http://*/*']);
assert.equal(await overlayEnabled.context.configureInlineAnnotations(false), true);
assert.equal(overlayEnabled.local.inlineAnnotationsEnabled, false);
assert.ok(overlayEnabled.unregisteredScriptIDs.includes('arivu-inline-annotation'));

const overlayDenied = runBackground({ permissionGranted: false });
assert.equal(await overlayDenied.context.configureInlineAnnotations(true), false);
assert.equal(overlayDenied.local.inlineAnnotationsEnabled, false);
assert.equal(overlayDenied.registeredScripts.length, 0);

const expiredSession = runBackground({ fetchStatus: 401 });
await assert.rejects(
  () => expiredSession.context.saveAnnotation({ url: 'https://example.com/expired', quote: 'Selected passage' }),
  /Session expired/,
);
assert.deepEqual(expiredSession.removedSessionKeys, ['accessToken', 'refreshToken']);

const disconnected = runBackground();
delete disconnected.session.accessToken;
await assert.rejects(
  () => disconnected.context.saveAnnotation({ url: '', quote: '' }),
  /Open Arivu to reconnect/,
);
assert.equal(disconnected.fetches.length, 0);
