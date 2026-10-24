import { spawn } from 'node:child_process';
import { constants } from 'node:fs';
import { access } from 'node:fs/promises';
import { pathToFileURL } from 'node:url';
import { chromium } from 'playwright';
import { SANDBOX_ENV, assertRenderersSandboxed, browserMarker, isSandboxLaunchError, launchOptions, sandboxEnabled } from './sandbox.mjs';

// Checks the runtime and returns a report. chromium_sandbox is "active" only
// after a launched browser's renderer was seen inside Chromium's sandbox,
// "disabled" when ARIVU_CAPTURE_CHROMIUM_SANDBOX is off, and "not_verified"
// when the sandbox is on but no browser was launched.
export async function verifyRuntime({ launchBrowser = true } = {}) {
  const sandbox = sandboxEnabled();
  const report = { chromium: '', chromium_sandbox: sandbox ? 'not_verified' : 'disabled', renderers: [], monolith: '' };
  await access(chromium.executablePath(), constants.X_OK).catch(() => {
    throw new Error('capture Chromium is missing or not executable');
  });
  if (launchBrowser) {
    const marker = browserMarker();
    const browser = await chromium.launch(launchOptions(sandbox, {}, marker)).catch((error) => {
      if (sandbox && isSandboxLaunchError(error)) {
        throw new Error(`capture Chromium could not start its sandbox (${SANDBOX_ENV}=true). The capture container needs the seccomp profile capture/security/seccomp-chromium.json and, on AppArmor hosts, the arivu-capture AppArmor profile; see openwiki/operations/deployment.md ("Compose / PaaS")`);
      }
      throw new Error('capture Chromium could not be launched');
    });
    try {
      report.chromium = browser.version();
      if (sandbox) {
        await browser.newPage();
        const result = await assertRenderersSandboxed({ marker }).catch((error) => {
          throw new Error(`capture Chromium started but its renderers are not sandboxed: ${error.message}`);
        });
        report.renderers = result.details.map((entry) => entry.checks);
        report.chromium_sandbox = 'active';
      }
    } finally {
      await browser.close();
    }
  }
  const command = process.env.ARIVU_MONOLITH_PATH || 'monolith';
  const version = await commandOutput(command, ['--version']);
  if (!/(?:^|\s)2\.11\.6(?:\s|$)/.test(version)) throw new Error('capture requires Monolith 2.11.6');
  report.monolith = '2.11.6';
  return report;
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
  try {
    process.stdout.write(`${JSON.stringify(await verifyRuntime())}\n`);
  } catch (error) {
    process.stderr.write(`capture preflight failed: ${error.message}\n`);
    process.exit(1);
  }
}
