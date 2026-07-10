import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

function storageGet(values, keys) {
  if (Array.isArray(keys)) return Object.fromEntries(keys.filter((key) => key in values).map((key) => [key, values[key]]));
  if (typeof keys === 'string') return keys in values ? { [keys]: values[keys] } : {};
  return { ...values };
}

function runBackground({ fetchStatus = 200, permissionGranted = true } = {}) {
  const fetches = [];
  const removedSessionKeys = [];
  const registeredScripts = [];
  const unregisteredScriptIDs = [];
  const local = { apiUrl: 'https://arivu.example/api' };
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
      runtime: { onInstalled: { addListener: () => {} }, onStartup: { addListener: () => {} }, onMessage: { addListener: () => {} } },
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
    importScripts: () => {},
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
