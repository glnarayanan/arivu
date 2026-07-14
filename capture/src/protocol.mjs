import { once } from 'node:events';

export const MAX_REQUEST_BYTES = 64 * 1024;
export const MAX_ATTEMPT_TIMEOUT_MS = 10 * 60 * 1000;
export const MAX_NAVIGATION_TIMEOUT_MS = 2 * 60 * 1000;

const requestKeys = new Set([
  'version', 'url', 'token', 'proxy_socket', 'proxy_token', 'formats',
  'navigation_timeout_ms', 'max_file_bytes', 'max_total_bytes',
  'attempt_timeout_ms',
  'max_media_files', 'max_media_file_bytes', 'max_media_total_bytes',
]);
const formats = new Set(['screenshot', 'pdf', 'self_contained_html']);

export function validateRequest(value) {
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error('invalid_request');
  for (const key of Object.keys(value)) if (!requestKeys.has(key)) throw new Error('invalid_request');
  if (value.version !== 2 || !validURL(value.url) || !validToken(value.token) || !validToken(value.proxy_token)) throw new Error('invalid_request');
  if (typeof value.proxy_socket !== 'string' || value.proxy_socket.length < 1 || value.proxy_socket.length > 100 || value.proxy_socket.includes('\0')) throw new Error('invalid_request');
  if (!Array.isArray(value.formats) || new Set(value.formats).size !== value.formats.length || value.formats.some((item) => !formats.has(item))) throw new Error('invalid_request');
  for (const key of ['attempt_timeout_ms', 'navigation_timeout_ms', 'max_file_bytes', 'max_total_bytes', 'max_media_files', 'max_media_file_bytes', 'max_media_total_bytes']) {
    if (!Number.isSafeInteger(value[key]) || value[key] <= 0) throw new Error('invalid_request');
  }
  if (value.attempt_timeout_ms > MAX_ATTEMPT_TIMEOUT_MS || value.navigation_timeout_ms > MAX_NAVIGATION_TIMEOUT_MS) throw new Error('invalid_request');
  if (value.navigation_timeout_ms > value.attempt_timeout_ms) throw new Error('invalid_request');
  if (value.max_media_file_bytes > value.max_media_total_bytes || value.max_media_total_bytes > value.max_total_bytes || value.max_file_bytes > value.max_total_bytes) throw new Error('invalid_request');
  return value;
}

export async function readRequest(socket) {
  return new Promise((resolve, reject) => {
    let data = Buffer.alloc(0);
    const cleanup = () => {
      socket.off('data', onData);
      socket.off('end', onEnd);
      socket.off('error', onError);
    };
    const fail = () => {
      cleanup();
      reject(new Error('invalid_request'));
    };
    const onEnd = () => fail();
    const onError = () => fail();
    const onData = (chunk) => {
      data = Buffer.concat([data, chunk], data.length + chunk.length);
      if (data.length > MAX_REQUEST_BYTES) return fail();
      const newline = data.indexOf(10);
      if (newline < 0) return;
      if (newline !== data.length - 1) return fail();
      try {
        const request = validateRequest(JSON.parse(data.subarray(0, newline).toString('utf8')));
        cleanup();
        socket.pause();
        resolve(request);
      } catch {
        fail();
      }
    };
    socket.on('data', onData);
    socket.once('end', onEnd);
    socket.once('error', onError);
  });
}

export async function writeResponse(socket, response, payloads = []) {
  await write(socket, Buffer.from(`${JSON.stringify(response)}\n`));
  for (const payload of payloads) await write(socket, payload);
  socket.end();
  await once(socket, 'close');
}

function write(socket, value) {
  if (socket.destroyed) return Promise.reject(new Error('socket_closed'));
  if (socket.write(value)) return Promise.resolve();
  return new Promise((resolve, reject) => {
    const cleanup = () => {
      socket.off('drain', onDrain);
      socket.off('close', onClose);
      socket.off('error', onError);
    };
    const onDrain = () => { cleanup(); resolve(); };
    const onClose = () => { cleanup(); reject(new Error('socket_closed')); };
    const onError = () => onClose();
    socket.once('drain', onDrain);
    socket.once('close', onClose);
    socket.once('error', onError);
  });
}

function validToken(value) {
  return typeof value === 'string' && /^[A-Za-z0-9_-]{16,128}$/.test(value);
}

function validURL(value) {
  try {
    const url = new URL(value);
    return (url.protocol === 'http:' || url.protocol === 'https:') && Boolean(url.hostname) && !url.username && !url.password;
  } catch {
    return false;
  }
}
