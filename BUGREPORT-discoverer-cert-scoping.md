# Bug report: a single unrelated compose project with an invalid name breaks all HTTPS

## Summary

On a host that runs **any** Docker Compose project whose name is not a valid
DNS label (most commonly: it contains an **underscore**), `bootstrap.sh up`
brings the stack up but **no HTTPS works at all** — Traefik never gets a
wildcard cert. The offending project does not need to be on `dynomesh-net`;
merely existing on the same Docker host is enough.

This reproduced on a clean install where the host also ran an unrelated
project named `omnissa_libre_chat` (underscores). dockerdynomesh's own
services were healthy, but every `https://…` request failed the TLS handshake.

## Environment

- macOS (Docker Desktop, `desktop-linux` context), Docker Engine 29.4.0,
  Docker Compose 5.1.1
- Tailscale active (so the override binds Traefik on loopback + tailnet IP)
- Other compose projects running on the host, one named `omnissa_libre_chat`

## Symptoms

`docker compose logs discoverer` loops on:

```
reissue: projects=4 suffixes=1 ~sans=5 projects=[dockerdynomesh..research-instance-20251008161206]
reissue: error: certgen returned 400: invalid project: omnissa_libre_chat
boot: initial reconcile failed after 10 attempts: certgen reissue: certgen returned 400: invalid project: omnissa_libre_chat
```

`docker compose logs traefik`:

```
ERR Unable to parse certificate /shared/certs/wildcard.crt error="… failed to find any PEM data in certificate input"
ERR Error while creating certificate store … tlsStoreName=default
```

`/shared/certs/` is **empty** — `wildcard.crt`/`wildcard.key` were never
written. Any `https://…` request resets during the TLS handshake
(`SSL_ERROR_SYSCALL`). HTTP (:80) still serves the welcome page.

## Root cause

Three layers each turn one bad value into a total outage instead of a
skipped entry:

1. **The cert project set is not scoped to the mesh network.**
   `reconciler.listEligible` (`cmd/discoverer/reconcile.go`) calls
   `ContainerList` with **no network filter**, and `reconcileWith` derives the
   cert project set from that full list via `inspect.ProjectsOf(containers)`.
   So **every compose project on the host** is sent to certgen — including
   projects with zero containers on `dynomesh-net`. (Routes, by contrast, are
   correctly scoped via `inspect.FromContainer`, which returns `ok=false` when
   the container has no IP on the network. The two paths disagree.)
   Side effect even when names are valid: unrelated project names leak into
   the wildcard cert's SAN list.

2. **certgen 400s the whole batch on the first invalid label.**
   `internal/certgen/api.go` `handleReissue` loops over `body.Projects` and
   returns `400 invalid project: <p>` on the first one that fails
   `ValidateLabel` (underscores are rejected, correctly, for a DNS label).
   Because the reissue is atomic, one bad project name means **no cert is
   issued for any project**, so `wildcard.crt` is never written.

3. **render hard-fails the whole config on the first invalid hostname.**
   `internal/render/render.go` returns an error for an unsafe hostname rather
   than skipping that one container.

All three contradict the documented contract in `docs/hostname-rules.md`:

> Containers whose name or project produces an invalid hostname after
> lowercasing are skipped — no router is generated, and a log line explains
> why.

`bootstrap.sh` also has an unrelated crash on macOS's default bash (see Bug 2).

## Reproduction

1. On a macOS host, start any compose project with an underscore in its name,
   e.g. a directory `omnissa_libre_chat/` with a trivial `docker-compose.yml`,
   `docker compose up -d`. It does **not** need to join `dynomesh-net`.
2. `./bootstrap.sh up` in dockerdynomesh.
3. Observe the discoverer `400 invalid project` loop, empty `/shared/certs/`,
   and failing HTTPS.

## Suggested fix (and the local patch applied to keep this machine running)

Make each layer degrade gracefully, matching the documented "skip + log"
behavior. The minimal, principled change is in the discoverer:

- **Scope the cert project set to mesh members.** Compute projects only from
  containers attached to the target network (new `inspect.OnNetwork` helper),
  so the project set matches the already-network-scoped route set. This alone
  fixes the live outage (unrelated projects disappear) and stops leaking
  unrelated names into the cert SANs.
- **Skip invalid project labels (don't fail the batch).** Drop project names
  that fail `certgen.ValidateLabel`, with a one-time log line. A malformed
  project then simply goes unserved instead of taking down TLS for every other
  project.
- **Skip invalid route hostnames** in the reconcile loop before they reach
  `render`, so `render`'s validation stays defense-in-depth.

certgen's strict `400` was left as-is — it's a reasonable server-side
contract; the discoverer (the client) should not send invalid input.

Files touched by the local stopgap patch (with tests):

```
cmd/discoverer/reconcile.go       # meshProjects(): network-scope + skip invalid labels; skip invalid hostnames
cmd/discoverer/main.go            # two dedupe-log maps on the reconciler
internal/inspect/inspect.go       # OnNetwork(c, network) helper
internal/inspect/inspect_test.go  # TestOnNetwork
cmd/discoverer/reconcile_test.go  # TestReconcileScopesProjectSetToMeshNetwork, TestReconcileSkipsInvalidProjectLabel
```

After the patch, `discoverer` logs `reissue: ok` with
`projects=[dockerdynomesh]`, `wildcard.crt` is written, and HTTPS validates
against `root-ca.pem`.

### Note for users who later join an underscore-named project TO the mesh

The scoping fix excludes off-mesh projects. But if you join a project whose
name has underscores (e.g. `omnissa_libre_chat`) **to** `dynomesh-net`, that
project still can't get a SAN (underscores aren't valid in a DNS name). With
the patch it's skipped+logged instead of breaking everything, but to actually
serve it, set a valid `COMPOSE_PROJECT_NAME` (e.g. `omnissa-libre-chat`).
Consider documenting this, or normalizing `_`→`-` in derived hostnames.

---

## Bug 2 (separate, also patched locally): bootstrap.sh crashes on bash 3.2

`bootstrap.sh` is `#!/bin/bash` with `set -euo pipefail`. On macOS's default
`/bin/bash` (3.2.57), expanding an **empty** array as `"${profile_args[@]}"`
errors with `unbound variable`, so the script dies before `docker compose up`:

```
./bootstrap.sh: line 480: profile_args[@]: unbound variable
```

This fires on every run where `TAKEOVER_SUFFIXES` is empty (the default).
Fix: guard the expansion — `${profile_args[@]+"${profile_args[@]}"}`.

---

## Bug 3 (separate): the Tailscale setup/redirect host is unreachable over HTTPS

When Tailscale is active, `bootstrap.sh` (`write_setup_router`) generates
`traefik/dynamic/setup.yml` so that:

- the catchall routers 301-redirect every unmatched host to
  `http://setup.<machine>.<tailnet>/`, and
- `setup-http`/`setup-https` serve the welcome page for
  `Host(setup.<machine>.<tailnet>)` **or** `Host(<machine>.<tailnet>)`.

Two problems make the tailnet setup page unreachable over HTTPS by default:

1. **`setup.<machine>.<tailnet>` does not resolve.** Tailscale MagicDNS
   resolves the bare device name (`<machine>.<tailnet>`) only; it does not
   synthesize subdomains under a device. So the catchall's redirect target
   can't be looked up:
   ```
   $ dscacheutil -q host -a name setup.lettol96mk7.tail5c72.ts.net   # (no result)
   $ curl http://setup.lettol96mk7.tail5c72.ts.net/                  # curl: (6) Could not resolve host
   $ dscacheutil -q host -a name lettol96mk7.tail5c72.ts.net         # -> 100.69.222.25  (bare name OK)
   ```

2. **The bare device name is not in the cert SANs.** The tailnet SAN is
   `*.<machine>.<tailnet>` (from `internal/certgen/san.go`). A wildcard needs
   exactly one label, so it covers `setup.<machine>.<tailnet>` but **not** the
   bare `<machine>.<tailnet>`. Result:
   ```
   $ curl --cacert root-ca.pem https://lettol96mk7.tail5c72.ts.net/
   curl: (60) SSL: no alternative certificate subject name matches target host name
   ```

Net effect: `setup.<machine>.<tailnet>` has a valid cert but no DNS; the bare
`<machine>.<tailnet>` has DNS but no matching cert. Only **`http://<machine>.<tailnet>/`**
(plain HTTP) actually serves the welcome page. Any browser that follows the
catchall redirect lands on an unresolvable hostname.

### Fix applied (local-first redirect target)

The redirect target should be a host that resolves **with no DNS setup on any
machine** and is covered by the cert. The local setup host `setup.<suffix>`
(e.g. `setup.docker.localhost`) satisfies both: `.localhost` is loopback on
every OS (RFC 6761) and `*.<suffix>` already covers it. Patch in
`write_setup_router`:

- Add `Host(\`setup.<suffix>\`)` to the `setup-http` / `setup-https` rules.
- Point the `redirect-to-setup` middleware at `http://setup.<suffix>/`.
- Keep the tailnet hostnames as additional `Host(...)` matches (for remote
  access) but do not use them as the redirect target.

This makes the catchall always land on a reachable, valid-HTTPS page locally.

### Optionally, make the tailnet name reachable locally too

To also serve the machine's tailnet hostnames (`setup.<machine>.<tailnet>`,
`<svc>.<proj>.<machine>.<tailnet>`) on this host, resolve them to loopback via
the existing takeover dnsmasq + `/etc/resolver` mechanism — scoped to the
machine's **own** subdomain, never the whole tailnet:

- dnsmasq: `--address=/<machine>.<tailnet>/127.0.0.1`
- resolver: `/etc/resolver/<machine>.<tailnet>` → `nameserver 127.0.0.1` /
  `port <takeover-dns-port>`

macOS selects the resolver with the longest matching domain, and Tailscale
registers `<tailnet>` via `scutil` (a 3-label domain), so a 4-label
`/etc/resolver/<machine>.<tailnet>` wins for this machine's names while peer
names (`<other-machine>.<tailnet>`) still resolve via MagicDNS. The cert
already covers `*.<machine>.<tailnet>`. Do **not** route the whole
`<tailnet>` to loopback — that would hijack peer resolution.

Note: the purely local flow (`https://<service>.<project>.docker.localhost`)
is unaffected — it resolves to loopback and the `*.docker.localhost` SAN
matches, so local HTTPS validates correctly.
