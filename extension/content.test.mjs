import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

async function runContentScript(cookie) {
  const fetches = [];
  const messages = [];
  const listeners = {};
  const context = {
    chrome: { runtime: { sendMessage: (message) => messages.push(message) } },
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
      location: { origin: 'https://notes.example.com' },
    },
  };
  context.window.window = context.window;

  vm.createContext(context);
  vm.runInContext(readFileSync(new URL('./content.js', import.meta.url), 'utf8'), context);
  await new Promise((resolve) => setImmediate(resolve));

  return { fetches, listeners, messages };
}

const withCSRF = await runContentScript('theme=dark; csrf_token=csrf-123; session=ok');
assert.equal(withCSRF.fetches[0].url, 'https://notes.example.com/api/auth/extension-token');
assert.equal(withCSRF.fetches[0].options.method, 'POST');
assert.equal(withCSRF.fetches[0].options.credentials, 'include');
assert.equal(withCSRF.fetches[0].options.headers['X-CSRF-Token'], 'csrf-123');
assert.equal(withCSRF.messages[0].action, 'saveTokens');
assert.equal(withCSRF.messages[0].accessToken, 'access');
assert.equal(withCSRF.messages[0].refreshToken, 'refresh');

const withoutCSRF = await runContentScript('theme=dark; session=ok');
assert.equal(Object.keys(withoutCSRF.fetches[0].options.headers).length, 0);
