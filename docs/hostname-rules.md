# Hostname Rules

For a compose service `<service>` in project `<project>`, dockerdynomesh
generates these hostnames:

- Local: `<service>.<project>.<suffix>` (default suffix: `docker.localhost`)
- Tailnet: `<service>.<project>.<machine>.<tailnet>.ts.net`

For a non-compose container named `<name>`:

- Local: `<name>.<suffix>`
- Tailnet: `<name>.<machine>.<tailnet>.ts.net`

The local-resolution suffix is absent from tailnet hostnames. Tailscale
MagicDNS routes by machine name; the suffix exists only to make plain DNS
resolve `*.<suffix>` to loopback.

## Lowercase normalization

Hostnames are lowercased before routing. The compose project and service
labels from Docker can contain uppercase characters; discoverer lowercases
the derived hostname in `inspect.inferHostname` before passing it to render.
This matches how browsers canonicalize Host headers, so there is no
case-sensitivity mismatch in practice.

## Validation

Every component of a hostname is validated against:

```
^[a-z0-9][a-z0-9.-]*$
```

with additional constraints: no `..`, no trailing `.` or `-`. This regex is
applied to:

- The primary suffix (e.g. `docker.localhost`)
- Each takeover suffix
- The machine name derived from Tailscale's `DNSName` field (not
  `HostName`, which can contain spaces and non-ASCII)
- The tailnet domain

Containers whose name or project produces an invalid hostname after
lowercasing are skipped — no router is generated, and a log line explains
why. The validation is also applied in the render layer to catch any values
that slipped through, and in the welcome service to prevent shell injection
into the install script template.

## One-level wildcard limit

X.509 wildcards cover exactly one label. `*.docker.localhost` matches
`nginx.docker.localhost` but not `nginx.myproject.docker.localhost`. This is why
certgen issues per-project SANs: `*.myproject.docker.localhost` covers all
services in `myproject`, and `*.docker.localhost` covers standalone containers
not in any compose project.

The cert SAN list grows by two entries per project per suffix (one wildcard
per project, one root wildcard per suffix). For typical usage (a handful of
projects, one or two suffixes) the cert stays small.

## Port selection

1. Label `dynomesh.port=NNNN` on the container, if set.
2. Single exposed port, if there is exactly one.
3. First match of 80, 8080, 8000 in the exposed port list.

If none of these match, the container is skipped — no router generated.
See [adding-to-projects.md](adding-to-projects.md) for how to set the label.

## Mirroring a production domain

To mirror a production hostname shape like `*.demo.falconmode.com`:

```bash
COMPOSE_PROJECT_NAME=demo.falconmode docker compose up -d
```

Result with default suffix `docker.localhost`:

- Local:   `<service>.demo.falconmode.docker.localhost`
- Tailnet: `<service>.demo.falconmode.<machine>.<tailnet>.ts.net`

If you want local URLs identical to production (no `.docker.localhost` infix),
set `SUFFIX=localhost` in the dockerdynomesh `.env`:

- Local:   `<service>.demo.falconmode.localhost`
- Tailnet: `<service>.demo.falconmode.<machine>.<tailnet>.ts.net`

The `.localhost` TLD resolves to loopback on every OS without DNS setup.
`SUFFIX=localhost` is therefore safe and useful when production-identical
URLs matter more than the `orb.` prefix distinguishing local from prod.

## Override

Drop a Traefik dynamic-config file into `traefik/dynamic/` to add a custom
router for a container. See [adding-to-projects.md](adding-to-projects.md)
for an example.
