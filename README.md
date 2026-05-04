# dockerdynomesh

Reverse proxy, auto-TLS, and DNS-friendly hostnames for Docker containers.
Works on macOS and Ubuntu with any Docker runtime (OrbStack, Colima, Rancher
Desktop, Docker Desktop). Optionally extends to any peer on your Tailscale
network.

Inspired by the convenience of OrbStack's automatic HTTPS for containers —
generalized so you get the same experience regardless of which runtime
you're using, with the option to share it across your tailnet.

The default suffix is `docker.localhost`, which resolves to loopback on
every OS without DNS setup (RFC 6761). An opt-in takeover mode lets
dockerdynomesh also serve URLs under other suffixes (e.g. `*.orb.local`,
`*.colima.local`, or your own) — useful when `.localhost` isn't an option
on a given machine, or when you want URLs that line up with another
runtime's convention.

## How it works

Six containers run from one compose file: **certgen** manages a mkcert root
CA and a wildcard TLS cert; **discoverer** watches Docker events and writes
Traefik dynamic config; **traefik** terminates TLS and routes by hostname;
**welcome** serves the CA download page; **socket-proxy** fronts the Docker
socket with read-only access; and **dnsmasq** (optional) answers DNS for
takeover suffixes. Run `./bootstrap.sh up` once per machine to wire it
together. See [docs/architecture.md](docs/architecture.md) for the full
picture.

## Quick start

```bash
git clone https://github.com/leonletto/dockerdynomesh
cd dockerdynomesh
./bootstrap.sh up
```

Then join a container to `dynomesh-net` and open
`https://<service>.<project>.docker.localhost` in your browser. See
[docs/getting-started.md](docs/getting-started.md) for the complete
walkthrough.

## Documentation

- [docs/getting-started.md](docs/getting-started.md) — end-to-end setup from
  clone to first HTTPS container
- [docs/architecture.md](docs/architecture.md) — the six services, data flow,
  TLS chain, security boundary
- [docs/adding-to-projects.md](docs/adding-to-projects.md) — how to join an
  existing compose project to the mesh
- [docs/hostname-rules.md](docs/hostname-rules.md) — how hostnames are derived,
  port selection, production mirroring
- [docs/configuration.md](docs/configuration.md) — every env var, what reads
  it, when to change it
- [docs/takeover-suffixes.md](docs/takeover-suffixes.md) — opt-in additional
  hostname suffixes (e.g. `*.orb.local`, `*.colima.local`) and the
  conflict-detection gate
- [docs/security.md](docs/security.md) — socket-proxy isolation, CA trust
  model, known limits
- [docs/troubleshooting.md](docs/troubleshooting.md) — common failures with
  diagnostic commands

## Status

Pre-release. Tested on macOS Tahoe (Sequoia-era kernel) and Ubuntu 22.04.
The Tailscale integration requires `tailscale` on the host PATH; it degrades
gracefully when absent.

## License

Apache-2.0. See [LICENSE](LICENSE).
