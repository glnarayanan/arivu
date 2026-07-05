import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

const context = { URL, globalThis: {} };
context.globalThis = context;
vm.createContext(context);
vm.runInContext(readFileSync(new URL('./url-utils.js', import.meta.url), 'utf8'), context);

const utils = context.ArivuExtensionURL;

assert.equal(utils.normalizeApiUrl('https://notes.example.com'), 'https://notes.example.com/api');
assert.equal(utils.normalizeApiUrl('https://notes.example.com/base/api/'), 'https://notes.example.com/base/api');
assert.equal(utils.apiOriginPattern('http://localhost:8001/api'), 'http://localhost/*');
assert.equal(utils.apiOriginPattern('https://notes.example.com:8443/api'), 'https://notes.example.com/*');
assert.equal(utils.builtInApiOrigin('https://arivu.app/*'), true);
assert.equal(utils.builtInApiOrigin('https://notes.example.com/*'), false);
assert.equal(utils.senderOriginAllowed('https://notes.example.com/auth', 'https://notes.example.com:8443/api'), false);
assert.equal(utils.senderOriginAllowed('https://notes.example.com:8443/auth', 'https://notes.example.com:8443/api'), true);
assert.equal(utils.senderOriginAllowed('http://localhost:8001/auth', 'https://notes.example.com/api'), true);
assert.equal(utils.senderOriginAllowed('https://evil.example.com/auth', 'https://notes.example.com/api'), false);
assert.throws(() => utils.normalizeApiUrl('file:///tmp/arivu'));
