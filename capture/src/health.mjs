import net from 'node:net';
import { verifyRuntime } from './preflight.mjs';

await verifyRuntime({ launchBrowser: false });
const socketPath = process.env.ARIVU_CAPTURE_SOCKET || process.argv[2];
if (!socketPath) throw new Error('capture socket path is required');
await new Promise((resolve, reject) => {
  const socket = net.createConnection(socketPath);
  socket.once('connect', () => {
    socket.end();
    resolve();
  });
  socket.once('error', reject);
});
