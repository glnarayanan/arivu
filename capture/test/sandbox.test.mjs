import assert from 'node:assert/strict';
import { mkdir, mkdtemp, readFile, rm, symlink, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { test } from 'node:test';
import { checkProfile } from '../security/seccomp-profile.mjs';
import { SANDBOX_ERROR_CODE, assertRenderersSandboxed, browserMarker, inspectRenderers, isSandboxLaunchError, launchOptions, sandboxEnabled } from '../src/sandbox.mjs';

const MARKER = '--arivu-browser-id=00000000-0000-4000-8000-000000000001';
const OTHER = '--arivu-browser-id=00000000-0000-4000-8000-000000000002';

test('the sandbox setting defaults to off and rejects unknown values', () => {
  assert.equal(sandboxEnabled({}), false);
  for (const value of ['1', 'true', 'TRUE', ' yes ', 'on']) assert.equal(sandboxEnabled({ ARIVU_CAPTURE_CHROMIUM_SANDBOX: value }), true);
  for (const value of ['', '0', 'false', 'no', 'off']) assert.equal(sandboxEnabled({ ARIVU_CAPTURE_CHROMIUM_SANDBOX: value }), false);
  assert.throws(() => sandboxEnabled({ ARIVU_CAPTURE_CHROMIUM_SANDBOX: 'maybe' }), /must be true or false/);
});

test('launch options pass the sandbox choice straight to Playwright', () => {
  assert.deepEqual(launchOptions(true, { args: ['--disable-quic'] }), { args: ['--disable-quic'], headless: true, chromiumSandbox: true });
  assert.deepEqual(launchOptions(true, { args: ['--disable-quic'] }, MARKER).args, ['--disable-quic', MARKER]);
  assert.match(browserMarker(), /^--arivu-browser-id=[0-9a-f-]{36}$/);
  assert.notEqual(browserMarker(), browserMarker());
  assert.equal(launchOptions(false).chromiumSandbox, false);
  assert.equal(launchOptions(true, { chromiumSandbox: false }).chromiumSandbox, true);
  assert.equal(isSandboxLaunchError(new Error('Browser logs:\nChromium sandboxing failed!')), true);
  assert.equal(isSandboxLaunchError(new Error('Browser logs:\nNo usable sandbox!')), true);
  assert.equal(isSandboxLaunchError(new Error('spawn ENOENT')), false);
});

// A fake /proc: helper (pid 10) -> browser 20 -> zygote 21 -> sandboxed
// zygote 22 -> renderer 23, with the namespace links and status fields the
// kernel exposes. Browser 40 is a second, unsandboxed browser under the same
// helper (a concurrent capture), which must not count for browser 20.
async function fakeProc(renderer = {}) {
  const proc = await mkdtemp(join(tmpdir(), 'arivu-fake-proc-'));
  const host = { user: 'user:[1]', pid: 'pid:[1]', net: 'net:[1]', root: '/', nspid: '20', filters: 1 };
  const sandboxed = { user: 'user:[2]', pid: 'pid:[2]', net: 'net:[2]', root: '/proc/22/fdinfo', nspid: '23\t4\t1', filters: 2 };
  const entries = [
    { pid: 20, ppid: 10, args: ['/browsers/chrome-headless-shell', '--headless', '--remote-debugging-pipe', MARKER], view: host },
    { pid: 21, ppid: 20, args: ['/browsers/chrome-headless-shell', '--type=zygote'], view: host },
    { pid: 22, ppid: 21, args: ['/browsers/chrome-headless-shell', '--type=zygote'], view: { ...sandboxed, nspid: '22\t1' } },
    { pid: 23, ppid: 22, args: ['/browsers/chrome-headless-shell', '--type=renderer'], view: { ...sandboxed, ...renderer } },
    { pid: 30, ppid: 1, args: ['/other/chrome', '--remote-debugging-pipe'], view: host },
    { pid: 31, ppid: 30, args: ['/other/chrome', '--type=renderer'], view: host },
    { pid: 40, ppid: 10, args: ['/browsers/chrome-headless-shell', '--headless', '--remote-debugging-pipe', OTHER], view: host },
    { pid: 41, ppid: 40, args: ['/browsers/chrome-headless-shell', '--type=renderer'], view: host },
  ];
  for (const { pid, ppid, args, view } of entries) {
    const dir = join(proc, String(pid));
    await mkdir(join(dir, 'ns'), { recursive: true });
    // Like Chromium: the browser keeps NUL-separated argv; zygote children
    // rewrite theirs into one space-separated string.
    await writeFile(join(dir, 'cmdline'), args.some((arg) => arg.startsWith('--type=')) ? `${args.join(' ')}\0` : `${args.join('\0')}\0`);
    await writeFile(join(dir, 'status'), `Name:\tchrome\nPPid:\t${ppid}\nNSpid:\t${view.nspid}\nSeccomp:\t2\nSeccomp_filters:\t${view.filters}\n`);
    await symlink(view.user, join(dir, 'ns', 'user'));
    await symlink(view.pid, join(dir, 'ns', 'pid'));
    await symlink(view.net, join(dir, 'ns', 'net'));
    await symlink(view.root, join(dir, 'root'));
  }
  await mkdir(join(proc, 'self'));
  return proc;
}

test('a renderer in its own namespaces, chroot, and seccomp filter counts as sandboxed', async (t) => {
  const proc = await fakeProc();
  t.after(() => rm(proc, { recursive: true, force: true }));
  const report = await inspectRenderers({ marker: MARKER, ancestor: 10, proc });
  assert.equal(report.renderers, 1, 'only renderers of the marked browser are inspected');
  assert.equal(report.sandboxed, true);
  assert.deepEqual(report.details[0].checks, { user_namespace: true, pid_namespace: true, network_namespace: true, chroot: true, seccomp_bpf: true });
  assert.equal((await assertRenderersSandboxed({ marker: MARKER, ancestor: 10, proc })).sandboxed, true);
});

for (const [name, change, missing] of [
  ['shares the browser user namespace', { user: 'user:[1]' }, 'user_namespace'],
  ['shares the browser pid namespace', { pid: 'pid:[1]', nspid: '23' }, 'pid_namespace'],
  ['shares the browser network namespace', { net: 'net:[1]' }, 'network_namespace'],
  ['is not chrooted', { root: '/' }, 'chroot'],
  ['has no extra seccomp filter', { filters: 1 }, 'seccomp_bpf'],
]) {
  test(`a renderer that ${name} fails closed`, async (t) => {
    const proc = await fakeProc(change);
    t.after(() => rm(proc, { recursive: true, force: true }));
    const report = await inspectRenderers({ marker: MARKER, ancestor: 10, proc });
    assert.equal(report.sandboxed, false);
    assert.match(report.failures.join(), new RegExp(missing));
    await assert.rejects(assertRenderersSandboxed({ marker: MARKER, ancestor: 10, proc }), (error) => error.code === SANDBOX_ERROR_CODE && error.message.includes(missing));
  });
}

test('no renderer to verify fails closed', async (t) => {
  const proc = await fakeProc();
  t.after(() => rm(proc, { recursive: true, force: true }));
  await assert.rejects(assertRenderersSandboxed({ marker: MARKER, ancestor: 99, proc, timeoutMs: 50 }), (error) => error.code === SANDBOX_ERROR_CODE && /no Chromium renderer/.test(error.message));
});

test('another browser\'s sandboxed renderer cannot vouch for this one', async (t) => {
  const proc = await fakeProc();
  t.after(() => rm(proc, { recursive: true, force: true }));
  const other = await inspectRenderers({ marker: OTHER, ancestor: 10, proc });
  assert.equal(other.renderers, 1);
  assert.equal(other.sandboxed, false, 'browser 40 is judged only by its own renderer');
  await assert.rejects(assertRenderersSandboxed({ marker: '--arivu-browser-id=missing', ancestor: 10, proc, timeoutMs: 50 }), /no Chromium renderer/);
  await assert.rejects(inspectRenderers({ ancestor: 10, proc }), /needs the browser marker/);
});

test('the seccomp profile is Docker 29.9.2 default plus only the documented changes', async () => {
  const profile = JSON.parse(await readFile(new URL('../security/seccomp-chromium.json', import.meta.url), 'utf8'));
  checkProfile(profile);
  assert.equal(profile.defaultAction, 'SCMP_ACT_ERRNO');
  const tampered = structuredClone(profile);
  tampered.syscalls[0].names.push('mount');
  assert.throws(() => checkProfile(tampered), /not moby\/profiles/);
  const widened = structuredClone(profile);
  widened.syscalls.find((rule) => rule.names[0] === 'unshare' && rule.comment).args[0].value = 0;
  assert.throws(() => checkProfile(widened), /differ from the documented/);
});

test('the AppArmor profile keeps docker-default and adds only userns and unix', async () => {
  const text = await readFile(new URL('../security/apparmor-arivu-capture', import.meta.url), 'utf8');
  const rules = text.split('\n').map((line) => line.replace(/#.*/, '').trim()).filter(Boolean);
  assert.ok(rules.includes('abi <abi/4.0>,'));
  assert.ok(rules.includes('profile arivu-capture flags=(attach_disconnected,mediate_deleted) {'));
  for (const rule of ['userns,', 'unix,', 'network,', 'deny network alg,', 'deny network vsock,', 'deny mount,', 'deny /sys/kernel/security/** rwklx,', 'ptrace (trace,tracedby,read,readby) peer=arivu-capture,']) {
    assert.ok(rules.includes(rule), `missing ${rule}`);
  }
  assert.ok(!rules.some((rule) => /\bunconfined\)|flags=\([^)]*(complain|unconfined)/.test(rule)), 'profile must enforce');
  assert.ok(!rules.some((rule) => /^mount\b|^pivot_root\b|^change_profile\b/.test(rule)), 'no mount, pivot_root, or change_profile grants');
});
