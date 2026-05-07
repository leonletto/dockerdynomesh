# Adding dockerdynomesh to an existing compose project

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

Bring the stack up with `docker compose up -d`. dockerdynomesh routes
`https://nginx.myproject.docker.localhost` to your container automatically.
If Tailscale was detected at bootstrap time,
`https://nginx.myproject.<machine>.<tailnet>.ts.net` works from any peer on
your tailnet with no additional setup.

See [hostname-rules.md](hostname-rules.md) for how hostnames are derived and
[configuration.md](configuration.md) for the env vars that control the suffix
and Tailscale identity.

## Controlling the port

By default, discoverer picks a port using this precedence:

1. The `dynomesh.port=NNNN` label on the container, if set.
2. The single exposed port, if there is exactly one.
3. The first match of 80, 8080, 8000 in the exposed port list.

If none of these match, the container is skipped — no router is generated.

To point the router at a specific port:

```yaml
services:
  api:
    image: myapp
    expose: ["3000", "9000"]   # two ports; without the label, skipped
    labels:
      dynomesh.port: "3000"
    networks: [default, dynomesh]
```

## Dropping a custom Traefik config file

For anything the auto-generated config doesn't cover — additional middlewares,
path-based routing, custom headers — drop a Traefik dynamic-config file into
`traefik/dynamic/` in the dockerdynomesh repo directory. The file provider
picks it up automatically via the file watcher; no restart needed.

Example: add basic auth to a container that auto.yml already routes:

```yaml
# traefik/dynamic/myapp-auth.yml
http:
  middlewares:
    myapp-auth:
      basicAuth:
        users:
          - "admin:$apr1$..."   # htpasswd output
  routers:
    nginx-myproject:            # must match the auto-generated router name
      middlewares:
        - myapp-auth
```

Router names in `auto.yml` follow the pattern `<service>-<project>`, with
dots replaced by hyphens. Check `traefik/dynamic/auto.yml` to see the exact
names for your containers.

> Custom hostnames outside `*.<project>.<suffix>` and
> `*.<project>.<machine>.<tailnet>.ts.net` aren't covered by the
> auto-generated wildcard cert. If you need a custom hostname, add it to
> a separate cert or accept a browser warning for that host only.

## Mirroring a production domain

To produce hostnames that match a production domain shape like
`*.demo.falconmode.com`, name your compose project after the production parent:

```bash
COMPOSE_PROJECT_NAME=demo.falconmode docker compose up -d
```

Result with the default suffix `docker.localhost`:

- Local:   `<service>.demo.falconmode.docker.localhost`
- Tailnet: `<service>.demo.falconmode.<machine>.<tailnet>.ts.net`

To drop the `.docker.localhost` infix and produce URLs identical to production,
set `SUFFIX=localhost` in the dockerdynomesh `.env`:

- Local:   `<service>.demo.falconmode.localhost`
- Tailnet: `<service>.demo.falconmode.<machine>.<tailnet>.ts.net`

See [hostname-rules.md](hostname-rules.md#mirroring-a-production-domain) for
more detail.

## Containers with existing Traefik labels

If a container already has `traefik.*` labels (it's managed by Traefik's
docker provider directly), discoverer skips it. The auto-generated router
and the docker-provider router would conflict. Pick one or the other.

## Reusing a cloud-style compose

If your project already has a `docker-compose.yml` written for a cloud
Traefik deploy (label-driven, with `Host()` rules, entrypoints `web` /
`websecure`, TLS options, standard middlewares), you can run the same
file locally under dockerdynomesh with only `.env` swaps.

### How it works

dockerdynomesh runs Traefik with **two providers** at once:

- **File provider** — owns the discoverer-generated routes and the
  shipped `standard-middlewares.yml` / `standard-tls.yml`.
- **Docker provider** — owns any container with `traefik.enable=true`.

The discoverer skips any container with a `traefik.*` label, so a given
container is owned by exactly one provider. There is no double-routing.

### Recommended convention: env-var-parameterized labels

Use env vars for the values that differ between cloud and local:

```yaml
labels:
  - "traefik.enable=true"
  - "traefik.http.routers.foo.rule=Host(`${HOST}.${DOMAIN}`)"
  - "traefik.http.routers.foo.entrypoints=websecure"
  - "traefik.http.routers.foo.tls=true"
  - "traefik.http.routers.foo.tls.options=${TLS_OPTIONS:-default@file}"
  - "traefik.http.routers.foo.middlewares=security-headers,compression"
  - "traefik.docker.network=${TRAEFIK_NETWORK:-dynomesh-net}"
```

Cloud `.env`:

```
DOMAIN=example.com
TLS_OPTIONS=cloudflare@file
TRAEFIK_NETWORK=traefik_network
```

Local `.env`:

```
DOMAIN=example.localhost
# defaults for TLS_OPTIONS and TRAEFIK_NETWORK kick in
```

### Stable names you can rely on locally

- Entrypoints: `web` (port 80) and `websecure` (port 443).
- Default docker-provider network: `dynomesh-net`.
- Shipped middlewares: `redirect-to-https`, `security-headers`,
  `compression` (in `standard-middlewares.yml`).
- Shipped TLS options: `default`, `cloudflare` (in `standard-tls.yml`).
- Cert: a wildcard SAN covers `*.<compose-project>.<suffix>`. If your
  compose project name contains dots (e.g. `demo.falconmode`), the SAN
  follows: `*.demo.falconmode.localhost`.

### Troubleshooting: labels look right but the host returns 404

The most common cause is that your service isn't attached to
`dynomesh-net`. Traefik's docker provider needs an IP on the
provider-level network (or one named in `traefik.docker.network=`) to
build a route — without one, it silently produces no route.

Look in the discoverer logs for a line like:

```
WARN container=foo project=bar has traefik.* labels but is not attached
to dynomesh-net. Traefik's docker provider will silently ignore it.
Fix: add `dynomesh-net` to the service's networks list, or set label
`traefik.docker.network=<your-network>`. Networks attached: [app-net]
```

Two ways to fix:

```yaml
# Option A — add dynomesh-net to the service
services:
  app:
    networks: [app-net, dynomesh-net]

networks:
  dynomesh-net:
    external: true
```

```yaml
# Option B — point the docker provider at your existing network
labels:
  - "traefik.docker.network=app-net"
```

### Adding `forward-auth` (recipe)

`forward-auth` isn't shipped because it requires a project-specific auth
backend (Authelia, oauth2-proxy, your own SSO). Add it project-locally:

1. Add the auth container to your compose file on `dynomesh-net`.
2. Create `traefik/dynamic/<project>-auth.yml` defining a `forward-auth`
   middleware that points at the auth container's URL.
3. Reference it from your service labels:
   `traefik.http.routers.foo.middlewares=forward-auth,security-headers`.
