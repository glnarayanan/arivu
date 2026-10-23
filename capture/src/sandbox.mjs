import { randomUUID } from 'node:crypto';
import { readdir, readFile, readlink } from 'node:fs/promises';

// ARIVU_CAPTURE_CHROMIUM_SANDBOX turns on Chromium's own sandbox. The capture
// container image sets it to true. Unset means off, which keeps native
// installs (systemd, no user namespaces) exactly as they were.
export const SANDBOX_ENV = 'ARIVU_CAPTURE_CHROMIUM_SANDBOX';
export const SANDBOX_ERROR_CODE = 'browser_sandbox_unavailable';

const truthy = new Set(['1', 'true', 'yes', 'on']);
const falsy = new Set(['', '0', 'false', 'no', 'off']);

export function sandboxEnabled(env = process.env) {
  const raw = (env[SANDBOX_ENV] ?? '').trim().toLowerCase();
  if (truthy.has(raw)) return true;
  if (falsy.has(raw)) return false;
  throw new Error(`${SANDBOX_ENV} must be true or false`);
}

// A unique switch on each browser's command line, so its own renderers can be
// found in /proc (Playwright does not expose the browser's pid). Chromium
// ignores switches it does not know.
export function browserMarker() {
  return `--arivu-browser-id=${randomUUID()}`;
}

// Playwright adds --no-sandbox only when chromiumSandbox is false. With it
// true, Chromium refuses to start if it cannot sandbox, and this helper never
// retries without the sandbox.
export function launchOptions(enabled, options = {}, marker = '') {
  const args = [...(options.args ?? []), ...(marker ? [marker] : [])];
  return { ...options, args, headless: true, chromiumSandbox: enabled };
}

// Chromium prints these when its sandbox cannot start (for example "Chromium
// sandboxing failed!" or "No usable sandbox!").
export function isSandboxLaunchError(error) {
  return /sandbox/i.test(String(error?.message ?? ''));
}

export function sandboxError(detail) {
  const error = new Error(`Chromium sandbox unavailable: ${detail}`);
  error.code = SANDBOX_ERROR_CODE;
  return error;
}

// Proves that the renderers of the browser launched with `marker` are
// sandboxed, by comparing each renderer with that browser process:
// - its own user, pid, and network namespaces (Chromium's namespace sandbox),
// - a chroot away from the container's root,
// - one more seccomp filter than the browser (Chromium's seccomp-bpf policy).
// Run it before untrusted content loads: a compromised process could rewrite
// its own /proc command line. Returns { renderers, sandboxed, failures }.
export async function inspectRenderers({ marker, ancestor = process.pid, proc = '/proc' } = {}) {
  if (!marker) throw new Error('inspectRenderers needs the browser marker');
  const processes = await listProcesses(proc);
  const byPid = new Map(processes.map((entry) => [entry.pid, entry]));
  const browsers = processes.filter((entry) => entry.type === 'browser' && entry.args.includes(marker) && descendsFrom(entry, ancestor, byPid));
  const renderers = [];
  const failures = [];
  for (const browser of browsers) {
    const browserView = await processView(proc, browser);
    if (!browserView) continue;
    for (const entry of processes) {
      if (entry.type !== 'renderer' || !descendsFrom(entry, browser.pid, byPid)) continue;
      const view = await processView(proc, entry);
      if (!view) continue; // exited while we looked
      const checks = {
        user_namespace: view.userns !== browserView.userns,
        pid_namespace: view.pidns !== browserView.pidns && view.nspidDepth > browserView.nspidDepth,
        network_namespace: view.netns !== browserView.netns,
        chroot: view.root !== browserView.root,
        seccomp_bpf: view.seccompFilters > browserView.seccompFilters,
      };
      renderers.push({ pid: entry.pid, checks });
      const missing = Object.entries(checks).filter(([, ok]) => !ok).map(([name]) => name);
      if (missing.length) failures.push(`renderer ${entry.pid} lacks ${missing.join(', ')}`);
    }
  }
  return { renderers: renderers.length, sandboxed: renderers.length > 0 && failures.length === 0, failures, details: renderers };
}

// Fails closed unless at least one renderer exists and every renderer is
// sandboxed. Renderers can take a moment to appear after a page opens.
export async function assertRenderersSandboxed({ timeoutMs = 3000, ...options } = {}) {
  const deadline = Date.now() + timeoutMs;
  for (;;) {
    const report = await inspectRenderers(options);
    if (report.failures.length) throw sandboxError(report.failures.join('; '));
    if (report.sandboxed) return report;
    if (Date.now() >= deadline) throw sandboxError('no Chromium renderer process found to verify');
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
}

async function listProcesses(proc) {
  const result = [];
  for (const name of await readdir(proc)) {
    if (!/^\d+$/.test(name)) continue;
    try {
      const [cmdline, status] = await Promise.all([
        readFile(`${proc}/${name}/cmdline`, 'utf8'),
        readFile(`${proc}/${name}/status`, 'utf8'),
      ]);
      // Zygote-forked processes rewrite their command line into one
      // space-separated string, so split on spaces as well as NULs.
      const args = cmdline.split(/[\0 ]+/);
      if (!args[0]) continue;
      const fields = parseStatus(status);
      const typeArg = args.find((arg) => arg.startsWith('--type='));
      const chromium = /chrom/i.test(args[0]);
      result.push({
        pid: Number(name),
        ppid: Number(fields.PPid),
        type: typeArg ? typeArg.slice('--type='.length) : (chromium && args.includes('--remote-debugging-pipe') ? 'browser' : ''),
        args,
      });
    } catch {
      // The process exited, or is not ours to read.
    }
  }
  return result;
}

async function processView(proc, entry) {
  try {
    const [status, userns, pidns, netns, root] = await Promise.all([
      readFile(`${proc}/${entry.pid}/status`, 'utf8'),
      readlink(`${proc}/${entry.pid}/ns/user`),
      readlink(`${proc}/${entry.pid}/ns/pid`),
      readlink(`${proc}/${entry.pid}/ns/net`),
      readlink(`${proc}/${entry.pid}/root`),
    ]);
    const fields = parseStatus(status);
    return {
      userns,
      pidns,
      netns,
      root,
      nspidDepth: (fields.NSpid ?? '').split(/\s+/).filter(Boolean).length,
      seccompFilters: Number(fields.Seccomp_filters ?? 0),
    };
  } catch {
    return undefined;
  }
}

function descendsFrom(entry, ancestor, byPid) {
  let current = entry;
  for (let depth = 0; current && depth < 64; depth += 1) {
    if (current.ppid === ancestor) return true;
    current = byPid.get(current.ppid);
  }
  return false;
}

function parseStatus(text) {
  const fields = {};
  for (const line of text.split('\n')) {
    const at = line.indexOf(':');
    if (at > 0) fields[line.slice(0, at)] = line.slice(at + 1).trim();
  }
  return fields;
}
