import { lstat, realpath } from 'node:fs/promises';
import net from 'node:net';
import { resolve, sep } from 'node:path';

export async function validateProxySocket(socketPath) {
  const configuredRoot = process.env.ARIVU_CAPTURE_RUNTIME_DIR;
  if (!configuredRoot) throw coded('capture_configuration_invalid');
  try {
    const root = await realpath(resolve(configuredRoot));
    const socket = await realpath(resolve(socketPath));
    const info = await lstat(socket);
    if (!info.isSocket() || !socket.startsWith(`${root}${sep}`)) throw coded('proxy_socket_invalid');
  } catch (error) {
    if (error?.code === 'capture_configuration_invalid' || error?.code === 'proxy_socket_invalid') throw error;
    throw coded('proxy_socket_invalid');
  }
}

export async function startProxyRelay(socketPath) {
  await validateProxySocket(socketPath);
  const connections = new Set();
  const server = net.createServer((client) => {
    const upstream = net.createConnection({ path: socketPath });
    connections.add(client);
    connections.add(upstream);
    const close = () => {
      client.destroy();
      upstream.destroy();
      connections.delete(client);
      connections.delete(upstream);
    };
    client.on('error', close);
    upstream.on('error', close);
    client.on('close', close);
    upstream.on('close', close);
    client.pipe(upstream);
    upstream.pipe(client);
  });
  await new Promise((resolve, reject) => {
    server.once('error', reject);
    server.listen(0, '127.0.0.1', resolve);
  });
  const address = server.address();
  return {
    port: address.port,
    async close() {
      for (const connection of connections) connection.destroy();
      await new Promise((resolve) => server.close(resolve));
    },
  };
}

function coded(code) {
  return Object.assign(new Error(code), { code });
}
