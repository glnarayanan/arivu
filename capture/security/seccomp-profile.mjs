#!/usr/bin/env node
// Builds capture/security/seccomp-chromium.json from Docker's default seccomp
// profile, and checks that the committed profile is exactly that default plus
// the changes listed below (see capture/security/README.md for the reasoning).
//
//   node capture/security/seccomp-profile.mjs build <moby default.json>   > capture/security/seccomp-chromium.json
//   node capture/security/seccomp-profile.mjs check capture/security/seccomp-chromium.json
//
// The base is moby/profiles seccomp v0.2.4, the profile Docker Engine 29.9.2
// compiles in as its builtin default:
// https://github.com/moby/moby/blob/docker-v29.9.2/vendor/github.com/moby/profiles/seccomp/default.json
import { createHash } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { pathToFileURL } from 'node:url';

export const BASE_SOURCE = 'moby/profiles seccomp v0.2.4 (Docker Engine 29.9.2 builtin default)';
// sha256 of JSON.stringify(JSON.parse(<base default.json>)): independent of
// whitespace, sensitive to every rule, value, and key order.
export const BASE_SHA256 = '4dc1298afa7676147c1320afa42d72691fc55379ae762b392b56853574a7a38a';

const CLONE_NEWNS = 0x00020000;
const CLONE_NEWCGROUP = 0x02000000;
const CLONE_NEWUTS = 0x04000000;
const CLONE_NEWIPC = 0x08000000;
const CLONE_NEWUSER = 0x10000000;
const CLONE_NEWPID = 0x20000000;
const CLONE_NEWNET = 0x40000000;
const CLONE_NEWTIME = 0x00000080; // unshare only; in clone() this bit is part of the exit signal
const AF_UNIX = 1;
const AF_INET = 2;
const AF_INET6 = 10;
const MARK = 'arivu:';

// Docker's own address-family rules for socket(), replaced below.
const BASE_SOCKET_RULES = [
  { names: ['socket'], action: 'SCMP_ACT_ALLOW', args: [{ index: 0, value: 38, op: 'SCMP_CMP_LT' }] },
  ...[39, 41, 42, 43, 44, 45].map((value) => ({ names: ['socket'], action: 'SCMP_ACT_ALLOW', args: [{ index: 0, value, op: 'SCMP_CMP_EQ' }] })),
];

export const SOCKET_RULES = [
  [AF_UNIX, 'AF_UNIX'],
  [AF_INET, 'AF_INET'],
  [AF_INET6, 'AF_INET6'],
].map(([value, name]) => ({
  names: ['socket'],
  action: 'SCMP_ACT_ALLOW',
  args: [{ index: 0, value, op: 'SCMP_CMP_EQ' }],
  comment: `${MARK} socket(${name}). Replaces Docker's address-family rules so the helper gets only AF_UNIX, AF_INET, and AF_INET6, like the native unit's RestrictAddressFamilies. No netlink, so a user namespace cannot reach netfilter or rtnetlink.`,
}));

export const ADDED_RULES = [
  {
    names: ['clone'],
    action: 'SCMP_ACT_ALLOW',
    args: [{ index: 0, value: CLONE_NEWNS | CLONE_NEWCGROUP | CLONE_NEWUTS | CLONE_NEWIPC, op: 'SCMP_CMP_MASKED_EQ' }],
    excludes: { arches: ['s390', 's390x'] },
    comment: `${MARK} clone() may create user, pid, and network namespaces (never mount, cgroup, uts, or ipc). Chromium's zygote starts every sandboxed process this way.`,
  },
  {
    names: ['unshare'],
    action: 'SCMP_ACT_ALLOW',
    args: [{ index: 0, value: CLONE_NEWNS | CLONE_NEWCGROUP | CLONE_NEWUTS | CLONE_NEWIPC | CLONE_NEWPID | CLONE_NEWNET | CLONE_NEWTIME, op: 'SCMP_CMP_MASKED_EQ' }],
    comment: `${MARK} unshare() may create only a user namespace. Chromium uses it to probe for and enter its namespace sandbox.`,
  },
  {
    names: ['chroot'],
    action: 'SCMP_ACT_ALLOW',
    comment: `${MARK} chroot(). Chromium chroots each sandboxed process into an empty directory to drop filesystem access. The kernel still requires CAP_SYS_CHROOT, which the container lacks, so it only works inside a user namespace the process created.`,
  },
];

export function buildProfile(base) {
  if (baseDigest(base) !== BASE_SHA256) throw new Error(`base profile is not ${BASE_SOURCE}`);
  const profile = structuredClone(base);
  const at = socketRulesIndex(profile.syscalls);
  profile.syscalls.splice(at, BASE_SOCKET_RULES.length, ...structuredClone(SOCKET_RULES));
  profile.syscalls.push(...structuredClone(ADDED_RULES));
  return profile;
}

// Reverses buildProfile: returns the base this profile must have come from.
export function baseFromProfile(profile) {
  const base = structuredClone(profile);
  const ours = base.syscalls.filter((rule) => rule.comment?.startsWith(MARK));
  const expected = [...SOCKET_RULES, ...ADDED_RULES];
  if (JSON.stringify(ours) !== JSON.stringify(expected)) throw new Error('profile changes differ from the documented arivu changes');
  const at = base.syscalls.findIndex((rule) => rule.comment?.startsWith(MARK));
  if (JSON.stringify(base.syscalls.slice(at, at + SOCKET_RULES.length)) !== JSON.stringify(SOCKET_RULES)) throw new Error('socket rules moved');
  base.syscalls.splice(at, SOCKET_RULES.length, ...structuredClone(BASE_SOCKET_RULES));
  base.syscalls = base.syscalls.filter((rule) => !rule.comment?.startsWith(MARK));
  return base;
}

export function baseDigest(base) {
  return createHash('sha256').update(JSON.stringify(base)).digest('hex');
}

export function checkProfile(profile) {
  const digest = baseDigest(baseFromProfile(profile));
  if (digest !== BASE_SHA256) throw new Error(`profile is not ${BASE_SOURCE} plus the documented changes (base sha256 ${digest})`);
}

function socketRulesIndex(syscalls) {
  const at = syscalls.findIndex((rule) => rule.names.length === 1 && rule.names[0] === 'socket');
  if (at < 0 || JSON.stringify(syscalls.slice(at, at + BASE_SOCKET_RULES.length)) !== JSON.stringify(BASE_SOCKET_RULES)) {
    throw new Error('base socket rules are not the expected Docker rules');
  }
  return at;
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  const [command, file] = process.argv.slice(2);
  const input = JSON.parse(readFileSync(file, 'utf8'));
  if (command === 'build') process.stdout.write(`${JSON.stringify(buildProfile(input), null, '\t')}\n`);
  else if (command === 'check') {
    checkProfile(input);
    process.stdout.write(`ok: ${file} is ${BASE_SOURCE} plus the documented arivu changes\n`);
  } else {
    process.stderr.write('usage: seccomp-profile.mjs build <moby default.json> | check <profile.json>\n');
    process.exit(2);
  }
}
