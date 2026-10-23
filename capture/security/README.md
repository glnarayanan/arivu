# Capture container security profiles

These two profiles let Chromium's own sandbox run inside the Compose `capture`
container without weakening the container. `compose.yaml` applies them to the
`capture` service only; the app container keeps Docker's defaults.

| File | Applied by | Base |
| --- | --- | --- |
| `seccomp-chromium.json` | `security_opt: seccomp=./capture/security/seccomp-chromium.json` (read by Compose at deploy time) | Docker Engine 29.9.2's builtin default, [moby/profiles seccomp v0.2.4](https://github.com/moby/moby/blob/docker-v29.9.2/vendor/github.com/moby/profiles/seccomp/default.json) |
| `apparmor-arivu-capture` | `security_opt: apparmor=${ARIVU_CAPTURE_APPARMOR_PROFILE:-arivu-capture}`; the operator installs it on the host | Docker's `docker-default` template, moby/profiles apparmor v0.2.2 |

Nothing else changes: `cap_drop: [ALL]`, `no-new-privileges:true`,
`network_mode: none`, `read_only: true`, uid 10002, the `/tmp` tmpfs, and the
memory and task limits all stay. Nothing is `unconfined`, nothing is
privileged, no capability is added, and there is no setuid sandbox helper.

## Why Docker's defaults block the sandbox

Chromium's Linux sandbox starts each renderer in new user, pid, and network
namespaces, chroots it into an empty directory, and then applies its own
seccomp-bpf policy. Tested with Playwright 1.64.1 (Chromium 156) on Docker
29.9.2:

- Docker's default seccomp profile allows `clone` with namespace flags,
  `unshare`, and `chroot` only when the container holds `CAP_SYS_ADMIN` or
  `CAP_SYS_CHROOT`. This container holds no capabilities, so Chromium prints
  "Chromium sandboxing failed!" and exits.
- On Ubuntu with `kernel.apparmor_restrict_unprivileged_userns=1`, AppArmor
  refuses to create a user namespace unless the confining profile allows it.
  `docker-default` does not.

## seccomp-chromium.json: every change

The profile is generated, not hand-edited:

```sh
curl -fsSL -o /tmp/moby-default.json \
  https://raw.githubusercontent.com/moby/moby/docker-v29.9.2/vendor/github.com/moby/profiles/seccomp/default.json
node capture/security/seccomp-profile.mjs build /tmp/moby-default.json > capture/security/seccomp-chromium.json
node capture/security/seccomp-profile.mjs check capture/security/seccomp-chromium.json
```

`check` (also run by `npm test` in `capture/`) strips the changes below and
compares the remainder with the pinned SHA-256 of Docker's profile, so the
committed file cannot drift from "Docker's default plus exactly these rules".
Each added rule carries a `comment` starting with `arivu:`.

| Change | Rule | Why |
| --- | --- | --- |
| Added | `clone` allowed when `flags & (CLONE_NEWNS \| CLONE_NEWCGROUP \| CLONE_NEWUTS \| CLONE_NEWIPC) == 0` (not on s390) | Chromium's zygote creates every sandboxed process with `CLONE_NEWUSER \| CLONE_NEWPID \| CLONE_NEWNET`. Tested: without `CLONE_NEWNET` the zygote dies with SIGTRAP; without `CLONE_NEWPID` the sandbox fails. Mount, cgroup, UTS, and IPC namespaces stay blocked. `clone3` keeps Docker's `ENOSYS`, so callers fall back to `clone`, which seccomp can inspect. |
| Added | `unshare` allowed when `flags & (CLONE_NEWNS \| CLONE_NEWCGROUP \| CLONE_NEWUTS \| CLONE_NEWIPC \| CLONE_NEWPID \| CLONE_NEWNET \| CLONE_NEWTIME) == 0` | Chromium calls `unshare(CLONE_NEWUSER)` to probe for and enter its namespace sandbox. Only a user namespace is allowed; tested that allowing nothing more is enough. |
| Added | `chroot` | Chromium chroots each sandboxed process into an empty directory (`/proc/self/fdinfo`) to drop filesystem access. The kernel still requires `CAP_SYS_CHROOT`, which the container lacks, so this only works inside a user namespace the process created. |
| Replaced | Docker's `socket` rules (any family below 38, plus 39 and 41–45) become `socket` with family `AF_UNIX`, `AF_INET`, or `AF_INET6` only | Matches the native unit's `RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6`. A process that creates a user and network namespace holds `CAP_NET_ADMIN` there; without netlink it cannot reach netfilter (nf_tables) or rtnetlink, the kernel surface most user-namespace exploits use. Chromium, Node, and Monolith need none of the dropped families. |

Still blocked, as in Docker's default: `mount`, `umount2`, `pivot_root`,
`setns`, `bpf`, `perf_event_open`, `ptrace` across processes, keyring and
module syscalls, `clone3` with namespaces, and everything else not listed.

A conditional *deny* for netfilter netlink alone was tried first and dropped:
libseccomp does not combine two comparisons on the same argument reliably
(it let AF_VSOCK through), so the profile uses plain single-value allows.

## apparmor-arivu-capture: every change

The profile body is the `docker-default` template as Docker renders it on
Ubuntu (with `tunables/global` and `abstractions/base`, and dockerd
unconfined), with three changes:

| Change | Why |
| --- | --- |
| `abi <abi/4.0>` instead of `abi/3.0` | ABI 4.0 is the first that understands the `userns` rule. |
| `userns,` | Lets Chromium create its user namespaces under Ubuntu's restriction. Processes inside them stay confined by this profile, and seccomp still decides which namespace types may be created. |
| `unix,` | Under ABI 3.0, `docker-default`'s `network,` also covered Unix sockets; under ABI 4.0 it does not. The helper's socket, the app's per-attempt proxy sockets, and Chromium's IPC need them. |

The template's separate dockerd signal line is omitted because dockerd runs
unconfined on Ubuntu, which makes it a repeat of `peer=unconfined`. Under ABI
4.0, rule classes `docker-default` leaves unmediated under 3.0, such as POSIX
message queues and io_uring, are denied here; nothing in the container uses
them, and Docker's seccomp profile already blocks io_uring.

The profile name `arivu-capture` appears in its `profile` line and its
`signal` and `ptrace` peers. To load it under another name, change all three
and set `ARIVU_CAPTURE_APPARMOR_PROFILE` to match.

Install, verify, and recovery steps are in
`openwiki/operations/deployment.md` ("Compose / PaaS").

## Updating

- **New Docker Engine default.** Fetch Moby's `default.json` for that version,
  update `BASE_SHA256` and `BASE_SOURCE` in `seccomp-profile.mjs`, rebuild,
  and rerun the sandbox checks. `build` refuses a base it does not know, and
  fails loudly if Docker's `socket` rules change shape.
- **New Chromium.** Run `node src/preflight.mjs` in the container. It must
  report `"chromium_sandbox":"active"`. If Chromium needs another syscall,
  add the narrowest rule to `seccomp-profile.mjs` with an `arivu:` comment
  and document it in the table above.
