import assert from 'node:assert/strict';
import { PassThrough } from 'node:stream';
import { test } from 'node:test';
import { assessQuality } from '../src/quality.mjs';
import { readRequest, validateRequest } from '../src/protocol.mjs';

function request() {
  return {
    version: 2,
    url: 'https://example.com/article',
    token: 'request_token_123456',
    proxy_socket: '/run/arivu-capture/egress.sock',
    proxy_token: 'proxy_token_12345678',
    formats: ['screenshot'],
    attempt_timeout_ms: 90000,
    navigation_timeout_ms: 30000,
    max_file_bytes: 1024,
    max_total_bytes: 4096,
    max_media_files: 4,
    max_media_file_bytes: 512,
    max_media_total_bytes: 2048,
  };
}

test('validates the strict bounded request contract', () => {
  assert.equal(validateRequest(request()).version, 2);
  for (const mutate of [
    (value) => { value.extra = true; },
    (value) => { value.url = 'file:///etc/passwd'; },
    (value) => { value.formats.push('mhtml'); },
    (value) => { value.formats.push('screenshot'); },
    (value) => { value.max_media_total_bytes = value.max_total_bytes + 1; },
    (value) => { value.proxy_socket = ''; },
  ]) {
    const value = request();
    mutate(value);
    assert.throws(() => validateRequest(value), /invalid_request/);
  }
});

test('classifies useful, partial, and challenged reader text deterministically', () => {
  assert.equal(assessQuality({ text: 'word '.repeat(250), title: 'Article', html: '<article />' }).status, 'complete');
  assert.equal(assessQuality({ text: 'word '.repeat(50), title: 'Article', html: '<article />' }).status, 'partial');
  const challenge = assessQuality({ text: 'Checking your browser', title: 'Just a moment', html: '<main />' });
  assert.equal(challenge.challenge, true);
  assert.ok(challenge.reasons.includes('challenge_detected'));
});

test('reads one request without destroying the response socket', async () => {
  const socket = new PassThrough();
  socket.write(`${JSON.stringify(request())}\n`);
  assert.equal((await readRequest(socket)).version, 2);
  assert.equal(socket.destroyed, false);
  socket.destroy();
});
