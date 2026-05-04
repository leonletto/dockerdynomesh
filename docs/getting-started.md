# Getting Started

End-to-end setup from a fresh clone to a running HTTPS container.

## Prerequisites

- **Docker** — OrbStack, Colima, Rancher Desktop, or Docker Desktop on macOS;
  the Docker Engine on Ubuntu. Any runtime that exposes
  `/var/run/docker.sock` works.
- **macOS or Ubuntu** — `bootstrap.sh` handles both. Other Linux distros work
  if you install `mkcert` and `jq` yourself before running it.
- **Homebrew** (macOS only) — bootstrap installs `mkcert` and `jq` via brew.
- **Tailscale** (optional) — if `tailscale` is on your PATH and has an active
  IP, bootstrap configures cross-machine hostnames automatically. If it's
  absent, everything still works locally.

## Run bootstrap

```bash
git clone https://github.com/leonletto/dockerdynomesh
cd dockerdynomesh
./bootstrap.sh up
```

Bootstrap does the following, in order:

1. Installs `mkcert` and `jq` if missing.
2. Detects Tailscale state (`tailscale ip -4`, `tailscale status --json`) and
   writes `.env` with `SUFFIX`, `TAILSCALE_IP`, `MACHINE_NAME`, and
   `TAILNET_DOMAIN`.
3. If Tailscale is present, generates `docker-compose.override.yml` to bind
   Traefik on both `127.0.0.1` and the Tailscale interface IP.
4. Generates `traefik/dynamic/setup.yml` — the welcome-page routers.
5. Creates the external Docker network `dynomesh-net` if it doesn't exist.
6. Runs `docker compose up -d --build`.
7. Waits up to 60 seconds for certgen to produce the root CA, then copies it
   to `./root-ca.pem`.

The whole process takes roughly 30–60 seconds on first run (image builds).
Subsequent runs are faster — nothing rebuilds unless the source changed.

Expected output (abbreviated):

```
==> Installing prerequisites...
==> Tailscale detected: 100.x.y.z (host-mbp.tailXXXX.ts.net)
==> Writing .env
==> Creating network dynomesh-net
==> Starting stack
[+] Running 5/5
 ✔ certgen        Started
 ✔ socket-proxy   Started
 ✔ discoverer     Started
 ✔ traefik        Started
 ✔ welcome        Started
Waiting for certgen to produce root cert...
Root CA exported to ./root-ca.pem
Install on macOS with:
  sudo security add-trusted-cert -d -r trustRoot -k /Library/Keychains/System.keychain root-ca.pem
```

## Install the root CA

The wildcard cert is signed by a local CA that mkcert generated inside the
`ca` Docker volume. Browsers won't trust it until you install the CA.

**macOS** (applies to Safari, Chrome, and other apps that use the system
keychain):

```bash
sudo security add-trusted-cert -d -r trustRoot \
  -k /Library/Keychains/System.keychain root-ca.pem
```

**Ubuntu** (applies to curl, wget, and system-level verification; Firefox
has its own store — see [troubleshooting](troubleshooting.md)):

```bash
sudo cp root-ca.pem /usr/local/share/ca-certificates/dockerdynomesh.crt
sudo update-ca-certificates
```

Bootstrap prints the right command for your OS. If you re-run bootstrap on a
machine where the CA is already trusted, it detects the fingerprint match and
skips the install reminder.

## Add a container

In your project's `docker-compose.yml`, declare the external network and
join the services that should be reachable:

```yaml
networks:
  default:
    name: ${COMPOSE_PROJECT_NAME:-myproject}_default
  dynomesh:
    name: dynomesh-net
    external: true

services:
  nginx:
    image: nginx
    networks: [default, dynomesh]
```

Bring the project up:

```bash
docker compose up -d
```

The discoverer sees the `start` event within a second, asks certgen to add
`*.nginx.docker.localhost` to the wildcard cert's SAN list, and writes a new
`auto.yml` to the Traefik file provider.

## Verify

Open `https://nginx.myproject.docker.localhost` in your browser. You should see
the nginx welcome page over a valid TLS connection (green padlock).

If Tailscale was detected, `https://nginx.myproject.host-mbp.tailXXXX.ts.net`
works from any peer on your tailnet — no extra setup required.

To confirm the router is present:

```bash
docker compose -f /path/to/dockerdynomesh/docker-compose.yml exec traefik \
  wget -qO- http://localhost:8080/api/rawdata | jq '.routers | keys'
```

Or just look at `traefik/dynamic/auto.yml` in the dockerdynomesh repo
directory — it's the live rendered config.

## Tear down

```bash
./bootstrap.sh down
```

This runs `docker compose down`. It does not remove the `dynomesh-net`
network (other projects may be using it), the named volumes (the CA is
preserved so a warm restart doesn't regenerate it), or any `/etc/resolver`
files if you enabled takeover mode. To remove resolver files, run
`./bootstrap.sh teardown-takeover` first.

## Where to go next

- [adding-to-projects.md](adding-to-projects.md) — labels, port selection,
  custom Traefik config files
- [hostname-rules.md](hostname-rules.md) — production domain mirroring and
  port selection precedence
- [configuration.md](configuration.md) — all env vars, what reads them
- [takeover-suffixes.md](takeover-suffixes.md) — opt-in additional hostname
  suffixes (e.g. `*.orb.local`, `*.colima.local`)
- [troubleshooting.md](troubleshooting.md) — if anything above didn't work
