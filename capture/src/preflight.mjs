import { spawn } from 'node:child_process';
import { constants } from 'node:fs';
import { access } from 'node:fs/promises';
import { pathToFileURL } from 'node:url';
import { chromium } from 'playwright';

export async function verifyRuntime({ launchBrowser = true } = {}) {
  await access(chromium.executablePath(), constants.X_OK).catch(() => {
    throw new Error('capture Chromium is missing or not executable');
  });
  if (launchBrowser) {
    const browser = await chromium.launch({ headless: true }).catch(() => {
      throw new Error('capture Chromium could not be launched');
    });
    await browser.close();
  }
  const command = process.env.ARIVU_MONOLITH_PATH || 'monolith';
  const version = await commandOutput(command, ['--version']);
  if (!/(?:^|\s)2\.10\.1(?:\s|$)/.test(version)) throw new Error('capture requires Monolith 2.10.1');
}

async function commandOutput(command, args) {
  const child = spawn(command, args, { stdio: ['ignore', 'pipe', 'pipe'] });
  const chunks = [];
  let size = 0;
  const collect = (chunk) => {
    size += chunk.length;
    if (size > 4096) child.kill('SIGKILL');
    else chunks.push(chunk);
  };
  child.stdout.on('data', collect);
  child.stderr.on('data', collect);
  const timer = setTimeout(() => child.kill('SIGKILL'), 5000);
  try {
    const code = await new Promise((resolve, reject) => {
      child.once('error', reject);
      child.once('close', resolve);
    });
    if (code !== 0 || size > 4096) throw new Error('capture Monolith could not be executed');
    return Buffer.concat(chunks).toString('utf8').trim();
  } finally {
    clearTimeout(timer);
  }
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  await verifyRuntime();
}
