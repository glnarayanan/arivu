#!/usr/bin/env node
import { chmod, lstat, mkdir, rm } from 'node:fs/promises';
import net from 'node:net';
import { dirname } from 'node:path';
import { capturePage, failedResponse } from './capture.mjs';
import { readRequest, writeResponse } from './protocol.mjs';

const socketPath = process.env.ARIVU_CAPTURE_SOCKET || process.argv[2];
if (!socketPath) {
  process.stderr.write('ARIVU_CAPTURE_SOCKET or a socket path argument is required\n');
  process.exit(2);
}

await mkdir(dirname(socketPath), { recursive: true });
try {
  const existing = await lstat(socketPath);
  if (!existing.isSocket()) throw new Error('capture socket path already exists and is not a socket');
  await assertSocketIsStale(socketPath);
  const current = await lstat(socketPath);
  if (current.dev !== existing.dev || current.ino !== existing.ino) throw new Error('capture socket changed during startup');
  await rm(socketPath);
} catch (error) {
  if (error.code !== 'ENOENT') throw error;
}
const attempts = new Set();
const server = net.createServer(async (socket) => {
  socket.on('error', () => {});
  let request;
  try {
    request = await readRequest(socket);
    const controller = new AbortController();
    attempts.add(controller);
    const abort = () => controller.abort();
    const timer = setTimeout(abort, request.attempt_timeout_ms);
    socket.once('end', abort);
    socket.once('close', abort);
    socket.resume();
    let result;
    try {
      result = await capturePage(request, controller.signal);
    } finally {
      clearTimeout(timer);
      socket.off('end', abort);
      socket.off('close', abort);
      attempts.delete(controller);
    }
    await writeResponse(socket, result.response, result.payloads);
  } catch (error) {
    if (process.env.ARIVU_CAPTURE_DEBUG === '1') process.stderr.write(`${error.stack ?? error.message}\n`);
    if (request && !socket.destroyed) await writeResponse(socket, failedResponse(request, error)).catch(() => socket.destroy());
    else socket.destroy();
  }
});
server.maxConnections = 2;
server.on('error', (error) => {
  process.stderr.write(`capture helper failed: ${error.message}\n`);
  process.exitCode = 1;
});
await new Promise((resolve, reject) => {
  server.once('error', reject);
  server.listen(socketPath, resolve);
});
await chmod(socketPath, 0o660);
const ownedSocket = await lstat(socketPath);

async function shutdown() {
  for (const controller of attempts) controller.abort();
  await new Promise((resolve) => server.close(resolve));
  try {
    const current = await lstat(socketPath);
    if (current.dev === ownedSocket.dev && current.ino === ownedSocket.ino) await rm(socketPath);
  } catch (error) {
    if (error.code !== 'ENOENT') throw error;
  }
}
process.once('SIGTERM', () => void shutdown());
process.once('SIGINT', () => void shutdown());

async function assertSocketIsStale(path) {
  await new Promise((resolve, reject) => {
    const probe = net.createConnection(path);
    probe.once('connect', () => {
      probe.destroy();
      reject(new Error('capture helper is already running'));
    });
    probe.once('error', (error) => {
      if (error.code === 'ECONNREFUSED' || error.code === 'ENOENT') resolve();
      else reject(error);
    });
  });
}
