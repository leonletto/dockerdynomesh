# Takeover Suffixes

dockerdynomesh's default makes containers reachable at `https://<service>.<project>.docker.localhost`. The `.localhost` TLD works without any DNS setup because RFC 6761 reserves it for loopback.

Takeover mode is opt-in and lets dockerdynomesh serve **additional** hostname suffixes alongside the default — e.g. `*.orb.local`, `*.colima.local`, or a custom suffix of your choosing. dockerdynomesh installs a small `dnsmasq` sidecar and a macOS `/etc/resolver/<suffix>` entry so the OS routes those names to loopback.

The default behavior of dockerdynomesh is unchanged.

## When to use this

- **`.localhost` isn't viable on this machine** — corporate network policies, captive portals, or VPN clients can intercept loopback names and prevent `*.localhost` from working the way you want.
- **You want URLs that match another runtime's convention** — e.g. you're using Colima or Rancher Desktop but want `*.orb.local` URLs because that's what your tooling, bookmarks, or scripts already point at.
- **You have legacy tooling pinned to a specific suffix** that you can't easily change.

## What it does

1. Adds extra hostnames per container — every container managed by dockerdynomesh becomes reachable at `<host>.<takeover-suffix>` in addition to `<host>.docker.localhost`.
2. Adds a small `dnsmasq` sidecar (only when this feature is enabled) that answers `*.<takeover-suffix>` → `127.0.0.1`. Configuration is written to `./dnsmasq.conf` (git-ignored) and mounted into the container — the image's entrypoint ignores CLI arguments.
3. Creates `/etc/resolver/<takeover-suffix>` so macOS routes lookups to that sidecar.
4. Adds `*.<takeover-suffix>` and `*.<project>.<takeover-suffix>` SANs to the wildcard cert.

## Enable

In `.env`:

```
TAKEOVER_SUFFIXES=orb.local
TAKEOVER_DNS_PORT=5300
```

Then run:

```
./bootstrap.sh up
```

You'll be prompted once (per suffix) before bootstrap creates `/etc/resolver/<suffix>` with `sudo`. To skip prompts in scripts, set `YES=1`.

For multiple suffixes, comma-separate: `TAKEOVER_SUFFIXES=orb.local,colima.local`.

## Verify

```
scutil --dns | grep -A 2 orb.local
dig @127.0.0.1 -p 5300 nginx.repo.orb.local
curl -v https://nginx.repo.orb.local
```

The `dig` should return `127.0.0.1`. The `curl` should produce a TLS handshake against your dockerdynomesh root and a response from the container.

## Teardown

```
./bootstrap.sh teardown-takeover
```

This stops the dnsmasq sidecar and removes the resolver files. To also drop the takeover SANs from the cert, blank `TAKEOVER_SUFFIXES` in `.env` and re-run `./bootstrap.sh up`. Two-step on purpose — removing resolver files breaks `*.orb.local` lookups instantly, which is the safe order; removing SANs is the followup.

`./bootstrap.sh down` does NOT remove resolver files. `down` stops the stack; it doesn't unwind host-level config you opted into.

## Conflict-detection gate

Before writing any `/etc/resolver` files, `takeover_setup()` probes whether OrbStack is already serving each requested suffix. If it is, two systems would race for the same name — so bootstrap refuses to proceed unless you opt in.

1. **Cheap detect** — checks whether the OrbStack process is running (`pgrep -x OrbStack`) or the `orb` CLI reports a live daemon (`orb status`). If neither is true the probe is skipped.

2. **Functional probe** — asks the system resolver (`dscacheutil`) for a name under the suffix (e.g. `nginx.probe.orb.local`). If the answer is a routable (non-loopback) IP, OrbStack is actively broadcasting mDNS for that suffix.

3. **If a conflict is detected** — bootstrap exits non-zero with:

   ```
   ERROR: OrbStack is already serving *.orb.local (probe: nginx.probe.orb.local -> 192.168.x.x).
   Installing a takeover for the same suffix would conflict with OrbStack's
   mDNS — some lookups would land here and some at OrbStack, with no
   guarantee which. Pick a different suffix, or set FORCE_TAKEOVER=1 to
   proceed anyway.
   ```

4. **If no conflict** — bootstrap proceeds silently. This includes the cases where OrbStack isn't installed, isn't running, or isn't answering for the requested suffix.

### Bypassing the gate

If you know what you're doing — for example, you've already disabled OrbStack's resolver and just want to finish the setup — set `FORCE_TAKEOVER=1`:

```
FORCE_TAKEOVER=1 ./bootstrap.sh up
```

This skips the probe entirely and prints a one-line warning. Use with caution: simultaneous OrbStack mDNS and dockerdynomesh resolver entries for the same suffix will cause intermittent resolution failures.

## Sharp edges

- **Port conflict on `TAKEOVER_DNS_PORT`** — if something else is on 5300, dnsmasq fails to start. Diagnose with `lsof -iTCP:5300 -iUDP:5300 -P` and bump the var (e.g. `5301`).
- **OrbStack partially running** — if OrbStack's daemon is still up and writing its own `/etc/resolver/orb.local`, the OS picks one. Bootstrap detects an existing file with different content and prompts before overwriting.
- **Cert reissue churn** — adding/removing entries in `TAKEOVER_SUFFIXES` reissues the cert. Browsers using HSTS may need to re-verify.
- **`.local` is mDNS-reserved** — `orb.local` works because `/etc/resolver/orb.local` overrides mDNS for that exact subdomain. Choosing other `.local` subdomains may interfere with normal mDNS expectations on your network.
- **Chrome shows `ERR_NAME_NOT_RESOLVED`** — Chrome (and other non-Apple browsers) needs macOS Local Network permission before it can reach `127.0.0.1`-bound services. The error name is misleading; the failure is at the connect layer, not DNS. Fix: System Settings → Privacy & Security → **Local Network** → enable **Google Chrome Helper** (and Google Chrome if listed), then quit Chrome (Cmd-Q) and relaunch. `tccutil reset LocalNetwork com.google.Chrome` does **not** work on macOS Sequoia/Tahoe — use the System Settings UI. Safari is exempt as a system app, so a working Safari does not mean Chrome will work. See [troubleshooting.md](troubleshooting.md) for the full explanation.
- **Multiple instances on one machine** — only one dockerdynomesh stack can bind `TAKEOVER_DNS_PORT`. The second instance must use a different port.
- **`/etc/resolver` cleared by macOS upgrade** — `bootstrap.sh up` is idempotent; re-running re-creates the files.
- **CA trust scope** — the takeover cert SANs are signed by the same local CA as everything else. Any peer on your tailnet that accesses a `*.<takeover-suffix>` hostname needs the root CA installed. See [security.md](security.md) for the CA trust model.
