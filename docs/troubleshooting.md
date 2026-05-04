# Troubleshooting

## Chrome shows ERR_NAME_NOT_RESOLVED for *.orb.local

**Symptom:** `https://nginx.myproject.orb.local` fails in Chrome with
`ERR_NAME_NOT_RESOLVED`. Safari on the same machine works fine.

**Cause:** Chrome (and other non-Apple browsers) requires macOS Local Network
permission before it can reach services bound to `127.0.0.1`. The error name
is misleading — DNS resolved fine. The failure is at the TCP connect step, and
Chrome reports it as a name resolution error.

**Fix:** System Settings → Privacy & Security → Local Network → enable
**Google Chrome Helper** (and Google Chrome if listed). Then quit Chrome with
Cmd-Q and relaunch. Using the menu bar X does not fully quit Chrome.

`tccutil reset LocalNetwork com.google.Chrome` does not work on macOS
Sequoia/Tahoe. Use the System Settings UI.

This symptom applies to takeover suffixes (`.orb.local`) specifically, not to
`.docker.localhost`. `.localhost` bypasses Local Network permission because the OS
resolves it to loopback before Chrome touches the network. See
[takeover-suffixes.md](takeover-suffixes.md) for more detail on the takeover
setup.

---

## Cert warning in Safari or Firefox after installing the CA

**Symptom:** macOS Keychain shows the dockerdynomesh root CA as trusted, but
Firefox still shows a cert warning. Safari works fine.

**Cause:** Firefox maintains its own certificate store and doesn't use the
macOS system keychain. The install command only affects the system keychain.

**Fix (Firefox on macOS):**

```bash
brew install nss   # installs certutil
mkcert -install    # adds the CA to Firefox's NSS database
```

`mkcert -install` finds Firefox's NSS profile automatically on most macOS
installations. You need to run it after `brew install nss` — without `nss`,
mkcert skips the Firefox step silently.

**Fix (Firefox on Ubuntu):**

```bash
sudo apt-get install -y libnss3-tools
mkcert -install
```

After installing, restart Firefox.

---

## `*.orb.local` lookups go to OrbStack instead of dockerdynomesh

**Symptom:** You set `TAKEOVER_SUFFIXES=orb.local` but
`nginx.myproject.orb.local` resolves to an OrbStack container IP instead of
`127.0.0.1`.

**Cause:** OrbStack is running and serving mDNS for `*.orb.local`. Two
systems can't own the same suffix cleanly, so dockerdynomesh's
conflict-detection gate either refused to install the takeover, or a
previous run left OrbStack's `/etc/resolver` entry in place.

**Check whether OrbStack is serving the suffix:**

```bash
dscacheutil -q host -a name nginx.probe.orb.local
# If it returns a non-127.x IP, OrbStack is answering for it.
```

**Options:**

1. Pick a different takeover suffix that OrbStack doesn't claim, e.g.
   `TAKEOVER_SUFFIXES=local.test` or `TAKEOVER_SUFFIXES=mycompany.local`.
   This is the cleanest option.

2. Stick with the default `docker.localhost` — it doesn't need a takeover
   and coexists with OrbStack on the same machine without any DNS conflict.

3. If you intentionally want dockerdynomesh to own `*.orb.local` (e.g.
   you're not running OrbStack on this machine but the gate is still
   triggering for some reason), bypass the gate:

   ```bash
   FORCE_TAKEOVER=1 ./bootstrap.sh up
   ```

   See [takeover-suffixes.md](takeover-suffixes.md#conflict-detection-gate)
   for the gate logic.

---

## discoverer logs nothing after a container starts

**Symptom:** You ran `docker compose up -d` in a project, but no new route
appeared and `traefik/dynamic/auto.yml` didn't change.

**Check discoverer logs:**

```bash
docker compose -f /path/to/dockerdynomesh/docker-compose.yml logs discoverer
```

Discoverer logs a line for every Docker event it receives:

```
event: network connect container=nginx
```

If no such lines appear, either:

- The container isn't on `dynomesh-net`. Verify with:
  ```bash
  docker inspect <container> | jq '.[].NetworkSettings.Networks | keys'
  ```
  It should include `dynomesh-net`.

- socket-proxy is not running or the discoverer can't reach it. Check:
  ```bash
  docker compose logs socket-proxy
  docker compose ps
  ```

If an event line appears but no reconcile follows, check for a port-selection
failure. Discoverer logs `inspect <id>: <reason>` when it skips a container.
The most common reason is no usable port — the container exposes multiple
ports and none is 80, 8080, or 8000, and no `dynomesh.port` label is set.

Fix: add the label to your service:

```yaml
labels:
  dynomesh.port: "3000"
```

---

## certgen socket healthcheck stuck

**Symptom:** `docker compose ps` shows certgen as unhealthy or starting for
more than a minute.

**Check certgen logs:**

```bash
docker compose -f /path/to/dockerdynomesh/docker-compose.yml logs certgen
```

The healthcheck tests for socket existence (`test -S /shared/run/certgen.sock`).
If certgen is failing to start, you'll see mkcert errors in its log. Common
causes:

- The `ca` volume is corrupted (partial write during a previous crash). Fix:
  ```bash
  docker compose down
  docker volume rm dockerdynomesh_ca dockerdynomesh_certs dockerdynomesh_run
  ./bootstrap.sh up
  ```
  This regenerates the CA — you'll need to re-install the root CA on any
  machines that trusted the old one.

- `mkcert` binary missing from the certgen image. This shouldn't happen with
  a clean build but can occur if the build cache is stale. Fix:
  ```bash
  docker compose build --no-cache certgen
  docker compose up -d certgen
  ```

---

## Tailscale routes don't work

**Symptom:** `https://nginx.myproject.host-mbp.tailXXXX.ts.net` times out
from a remote peer, or the hostname doesn't appear in `auto.yml` at all.

**Check the local stack first:**

```bash
tailscale status   # should show your machine with an IP
tailscale ip -4    # should return an IP in the 100.x.x.x range
```

If `tailscale ip -4` returns nothing, Tailscale is not connected. Re-run
`./bootstrap.sh up` after Tailscale reconnects; bootstrap re-detects the IP
and rewrites `.env` and `docker-compose.override.yml`.

**Check the .env:**

```bash
cat .env
```

`MACHINE_NAME` and `TAILNET_DOMAIN` should be non-empty. If they're empty,
bootstrap didn't detect Tailscale. Re-run bootstrap.

**Check Traefik's port bindings:**

```bash
docker inspect dockerdynomesh-traefik-1 | jq '.[].HostConfig.PortBindings'
```

You should see entries for both `127.0.0.1` and the Tailscale IP
(`100.x.x.x`). If only `127.0.0.1` appears, `docker-compose.override.yml`
is either missing or wasn't applied. Re-run `./bootstrap.sh up`.

**Check from the remote peer:**

```bash
# On the remote peer:
curl -v --insecure https://nginx.myproject.host-mbp.tailXXXX.ts.net
# If you get a cert warning (not a connection refused), the route is working
# but the CA isn't installed on the remote machine.
```

Download and install the root CA on the remote peer from
`http://setup.<machine>.<tailnet>/` before trusting the HTTPS cert.

---

## bootstrap.sh fails on Linux with "dpkg not found"

**Symptom:** Running `./bootstrap.sh up` on a non-Debian Linux fails with
`dpkg not found; cannot auto-detect arch for mkcert`.

**Cause:** bootstrap.sh uses `dpkg --print-architecture` to pick the right
mkcert binary on Linux. It only supports Debian/Ubuntu.

**Fix:** Install mkcert manually for your distribution:

```bash
# Example for amd64:
sudo curl -fsSL https://github.com/FiloSottile/mkcert/releases/download/v1.4.4/mkcert-v1.4.4-linux-amd64 \
  -o /usr/local/bin/mkcert
sudo chmod +x /usr/local/bin/mkcert
```

Then re-run `./bootstrap.sh up`.
