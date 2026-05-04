# Architecture

dockerdynomesh is six containers and a host bootstrap script. The containers
run from a single compose file. The host script runs once per machine.

## The six services

**certgen** owns the mkcert root CA and the wildcard TLS certificate. On
startup it initializes the CA in the `ca` named volume (creating it if absent,
reusing it if present), then listens on a Unix socket
(`/shared/run/certgen.sock`) for reissue requests from the discoverer. A
reissue request carries a list of compose project names and suffixes; certgen
expands them into the full SAN list and calls `mkcert` if the list changed or
the cert is within 30 days of expiry. The cert and key land in the `certs`
volume. certgen runs as root inside an isolated container; it has no network
exposure beyond the Unix socket shared via the `run` volume.

**socket-proxy** is a `tecnativa/docker-socket-proxy` sidecar that sits
between the Docker daemon and the discoverer. It binds `/var/run/docker.sock`
read-only and exposes only four API families over a plain TCP socket on
`dynomesh-net`: `EVENTS`, `CONTAINERS`, `NETWORKS`, and `INFO`. All write
endpoints (exec, run, build, commit, etc.) are denied by default. The
discoverer connects to `tcp://socket-proxy:2375` via `DOCKER_HOST`; it never
touches the host docker socket directly.

**discoverer** watches Docker events through socket-proxy and translates
container state into Traefik dynamic config. On boot it does an initial full
container scan with a bounded retry loop (up to 10 attempts, 500 ms apart) to
absorb certgen not being ready yet. After that it subscribes to container
`start`/`stop`/`die` events and network `connect`/`disconnect` events. Events
are debounced (default 500 ms) so a rapid sequence of starts produces one
reconcile, not ten. On each reconcile it derives the set of visible compose
projects, calls certgen to reissue if that set changed, waits for the reissue
to complete, then atomically writes `auto.yml` to the bind-mounted
`traefik/dynamic/` directory. The write is key → cert → auto.yml in order:
Traefik never sees a hostname the cert doesn't cover. discoverer runs as
distroless nonroot (UID 65532).

**traefik** terminates TLS and routes requests by `Host()` header. It reads
static config from `traefik/traefik.yml` (bind mount) and dynamic config from
the `traefik/dynamic/` directory via the file provider (file watcher, no
polling). The `certs` volume provides `wildcard.crt` and `wildcard.key`. Three
sources of dynamic config coexist: `auto.yml` (discoverer), `cert.yml`
(committed, declares the TLS store), and `setup.yml` (bootstrap-generated,
setup-page routers). Traefik binds `127.0.0.1:80` and `127.0.0.1:443` by
default; when Tailscale is detected, bootstrap appends the Tailscale interface
IP to the port bindings via `docker-compose.override.yml`.

**welcome** serves a small HTTP page that delivers the root CA download and
install instructions. It runs inside the compose network with no host-side
ports; Traefik routes `http://setup.<machine>.<tailnet>/` to it on port 80.
The page is HTTP-only by design: before the user has installed the CA,
HTTPS would show a cert warning. Once the CA is installed, containers show up
on HTTPS directly.

**dnsmasq** is optional and only starts under the `takeover` profile (set by
bootstrap when `TAKEOVER_SUFFIXES` is non-empty). It answers `*.<suffix>` →
`127.0.0.1` for whatever takeover suffixes you configured. bootstrap generates
`dnsmasq.conf` and mounts it read-only into the container. macOS resolver
entries in `/etc/resolver/<suffix>` point at it. See
[takeover-suffixes.md](takeover-suffixes.md).

## Data flow on cold boot

```
bootstrap.sh up
  │
  ├─ certgen starts
  │    └─ mkcert -install (root CA in ca volume)
  │    └─ listens on /shared/run/certgen.sock
  │
  ├─ socket-proxy starts (wraps /var/run/docker.sock)
  │
  ├─ discoverer starts
  │    └─ bootReconcile() with retry loop
  │         └─ lists containers on dynomesh-net
  │         └─ sends ReissueRequest to certgen
  │              └─ certgen runs mkcert → wildcard.crt in certs volume
  │         └─ renders auto.yml → traefik/dynamic/auto.yml
  │
  └─ traefik starts (waits for certgen socket healthcheck)
       └─ reads auto.yml + cert.yml via file watcher
       └─ loads wildcard.crt from certs volume
       └─ :80 and :443 open
```

Traefik's `service_healthy` dependency on certgen gates startup on the socket
existing, not on the wildcard cert existing. The cert appears after the first
discoverer reconcile. This is intentional: certgen can't issue the cert until
it knows which SANs to include, and only discoverer knows which containers are
present.

## Data flow when a container appears

1. A container joins `dynomesh-net` (via `docker compose up` in a user
   project).
2. Docker emits a `network connect` event; discoverer's debouncer fires after
   500 ms.
3. discoverer calls `ContainerList` + `ContainerInspect` through socket-proxy.
4. `inspect.FromContainer` extracts the compose project/service labels,
   derives a hostname (e.g. `nginx.myproject`), and picks a port via the
   `dynomesh.port` label, single-exposed-port, or 80/8080/8000 fallback.
5. The project set has changed, so discoverer sends a `ReissueRequest` to
   certgen. certgen expands `nginx.myproject` into SANs like
   `*.myproject.docker.localhost` and `*.myproject.host-mbp.tailXXXX.ts.net`,
   runs `mkcert`, and writes the new cert.
6. discoverer atomically renames `auto.yml.tmp` → `auto.yml` with the new
   router entry.
7. Traefik's file watcher picks up `auto.yml`; the route is live.

When the container stops (`die` or `stop` event), discoverer reconciles again.
If no containers from that project remain, the project drops from the SAN list
on the next reissue request, and the router entry disappears from `auto.yml`.

## The TLS chain

The root CA lives in the `ca` named volume at `/shared/ca/rootCA.pem` (and
`rootCA-key.pem`). It is generated once by mkcert and reused on every warm
start. bootstrap copies `rootCA.pem` to `./root-ca.pem` in the repo directory
so you can install it in your browser trust store.

The wildcard cert lives in the `certs` named volume at
`/shared/certs/wildcard.crt`. It is a single cert with one SAN per project per
suffix, plus the root wildcard for each suffix. Example SAN list for a stack
with projects `myproject` and `tools`, suffix `docker.localhost`, and Tailscale
machine `host-mbp.tailXXXX.ts.net`:

```
*.docker.localhost
*.myproject.docker.localhost
*.tools.docker.localhost
*.myproject.host-mbp.tailXXXX.ts.net
*.tools.host-mbp.tailXXXX.ts.net
```

The wildcards are one level deep because that is all X.509 allows. A cert
with `*.docker.localhost` covers `nginx.docker.localhost` but not
`nginx.myproject.docker.localhost`. The project-level SANs (`*.myproject.docker.localhost`)
are what make nested hostnames work.

The cert expiry is mkcert's default (roughly 2 years). certgen reissues when
the SAN list changes or the cert is within 30 days of expiry.

## Networks

`dynomesh-net` is an external Docker bridge network created by bootstrap.
"External" means compose doesn't own its lifecycle — you can join any number
of unrelated compose stacks to it.

Services within the dockerdynomesh stack attach to `dynomesh-net` for
inter-service communication (discoverer → socket-proxy, discoverer →
certgen socket volume, traefik → container upstreams). Traefik reaches user
containers at their `dynomesh-net` IP addresses directly; those containers
don't need to know about Traefik.

socket-proxy attaches only to `dynomesh-net` (no host port). It is
unreachable from outside the network; a container that isn't on `dynomesh-net`
can't reach the Docker API through it.

## Security boundary

The attack surface this stack is designed to constrain is the Docker socket.
Binding `/var/run/docker.sock` into a container that runs user-controlled
workloads gives those workloads a path to host root (spawn a privileged
container, escape). The mitigation here is the socket-proxy:

- The host socket is mounted only into socket-proxy (read-only).
- socket-proxy exposes only read-only endpoints on a private network.
- discoverer connects via `tcp://socket-proxy:2375` and runs as UID 65532
  (distroless nonroot). A compromised discoverer can read container metadata;
  it cannot exec into containers, pull images, or start privileged containers.

What this does not protect: traefik and certgen run as root inside their
containers. Their images are Debian-based (Traefik) and distroless (certgen).
Neither mounts the Docker socket. The threat model is: if a container proxied
through Traefik is compromised, the attacker reaches Traefik's reverse-proxy
layer, not the Docker daemon. That is the same isolation you get with any
Traefik deployment.

See [security.md](security.md) for the full posture and known limits.

## Logging

Each service logs to stdout (Docker default). Useful commands:

```bash
docker compose logs certgen       # CA init, reissue requests, mkcert output
docker compose logs discoverer    # boot config, every event, reconcile steps
docker compose logs traefik       # access log, routing decisions (if enabled)
docker compose logs socket-proxy  # Docker API request log
```

discoverer logs a startup banner with the resolved config on every boot:

```
boot: suffix=docker.localhost machine=host-mbp tailnet=tailXXXX.ts.net takeover_suffixes=<none> ...
```

On each reconcile it logs the projects, suffixes, and rough SAN count before
calling certgen:

```
reissue: projects=2 suffixes=1 ~sans=3 projects=[myproject..tools]
reissue: ok
```
