import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

async function runContentScript(cookie, bootstrap = { success: true, apiUrl: 'https://notes.example.com:8443/api' }) {
  const fetches = [];
  const messages = [];
  const listeners = {};
  const context = {
    chrome: { runtime: { sendMessage: async (message) => {
      messages.push(message);
      return message.action === 'tokenBootstrapContext' ? bootstrap : { success: true };
    } } },
    document: { cookie },
    fetch: async (url, options) => {
      fetches.push({ url, options });
      return {
        ok: true,
        json: async () => ({ access_token: 'access', refresh_token: 'refresh' }),
      };
    },
    window: {
      addEventListener: (name, handler) => {
        listeners[name] = handler;
      },
      location: { origin: 'https://unrelated.example' },
    },
  };
  context.window.window = context.window;

  vm.createContext(context);
  vm.runInContext(readFileSync(new URL('./content.js', import.meta.url), 'utf8'), context);
  await new Promise((resolve) => setImmediate(resolve));

  return { fetches, listeners, messages };
}

const withCSRF = await runContentScript('theme=dark; csrf_token=csrf-123; session=ok');
assert.equal(withCSRF.messages[0].action, 'tokenBootstrapContext');
assert.equal(withCSRF.fetches[0].url, 'https://notes.example.com:8443/api/auth/extension-token');
assert.equal(withCSRF.fetches[0].options.method, 'POST');
assert.equal(withCSRF.fetches[0].options.credentials, 'include');
assert.equal(withCSRF.fetches[0].options.headers['X-CSRF-Token'], 'csrf-123');
assert.equal(withCSRF.messages[1].action, 'saveTokens');
assert.equal(withCSRF.messages[1].accessToken, 'access');
assert.equal(withCSRF.messages[1].refreshToken, 'refresh');

const withoutCSRF = await runContentScript('theme=dark; session=ok');
assert.equal(Object.keys(withoutCSRF.fetches[0].options.headers).length, 0);

const rejectedBootstrap = await runContentScript('csrf_token=spoofed', { success: false });
assert.equal(rejectedBootstrap.fetches.length, 0);
assert.equal(rejectedBootstrap.messages.length, 1);
assert.equal(rejectedBootstrap.messages[0].action, 'tokenBootstrapContext');
assert.equal(rejectedBootstrap.listeners.message, undefined);
