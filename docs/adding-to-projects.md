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
