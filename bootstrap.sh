#!/bin/bash
# One-time per-machine setup for dockerdynomesh.
# Detects OS, installs prerequisites, fills in .env, creates the external
# Docker network, brings the stack up, and exports the root CA. Idempotent:
# re-running on a working machine refreshes Tailscale-derived state and
# restarts the stack with the new values.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$SCRIPT_DIR"

OS=$(uname -s)

ensure_brew() {
  if ! command -v brew >/dev/null 2>&1; then
    printf 'Homebrew not found. Install from https://brew.sh first.\n' >&2
    exit 1
  fi
}

install_prereq() {
  case "$OS" in
    Darwin)
      ensure_brew
      command -v mkcert >/dev/null 2>&1 || brew install mkcert
      command -v jq >/dev/null 2>&1 || brew install jq
      command -v docker >/dev/null 2>&1 || {
        printf 'Install Docker (OrbStack/Colima/Docker Desktop) before continuing.\n' >&2
        exit 1
      }
      command -v tailscale >/dev/null 2>&1 || \
        printf 'Tailscale not found; cross-machine features disabled.\n'
      ;;
    Linux)
      sudo apt-get update -y
      sudo apt-get install -y libnss3-tools curl jq
      if ! command -v mkcert >/dev/null 2>&1; then
        ARCH=$(dpkg --print-architecture 2>/dev/null) || {
          printf 'dpkg not found; cannot auto-detect arch for mkcert. Install mkcert manually and re-run.\n' >&2
          exit 1
        }
        sudo curl -fsSL "https://github.com/FiloSottile/mkcert/releases/download/v1.4.4/mkcert-v1.4.4-linux-${ARCH}" -o /usr/local/bin/mkcert
        sudo chmod +x /usr/local/bin/mkcert
      fi
      command -v docker >/dev/null 2>&1 || {
        printf 'Install Docker before continuing.\n' >&2
        exit 1
      }
      ;;
    *)
      printf 'Unsupported OS: %s\n' "$OS" >&2
      exit 1
      ;;
  esac
}

discover_tailscale() {
  TAILSCALE_IP=""
  MACHINE_NAME=""
  TAILNET_DOMAIN=""
  if command -v tailscale >/dev/null 2>&1; then
    TAILSCALE_IP=$(tailscale ip -4 2>/dev/null | head -n1 || true)
    if [ -n "$TAILSCALE_IP" ] && command -v jq >/dev/null 2>&1; then
      # Derive MACHINE_NAME from DNSName (DNS-safe slug Tailscale assigns),
      # not HostName (the user-friendly name, which can contain spaces and
      # smart quotes — certgen rejects those as invalid).
      DNS_FULL=$(tailscale status --self --json 2>/dev/null | jq -r '.Self.DNSName // empty' || true)
      DNS_FULL=${DNS_FULL%.}
      MACHINE_NAME=${DNS_FULL%%.*}
      TAILNET_DOMAIN=$(tailscale status --json 2>/dev/null | jq -r '.CurrentTailnet.MagicDNSSuffix // empty' || true)
    fi
  fi
}

write_env() {
  cat > .env <<EOF
SUFFIX=${SUFFIX:-docker.localhost}
TAILSCALE_IP=$TAILSCALE_IP
MACHINE_NAME=$MACHINE_NAME
TAILNET_DOMAIN=$TAILNET_DOMAIN
TAKEOVER_SUFFIXES=${TAKEOVER_SUFFIXES:-}
TAKEOVER_DNS_PORT=${TAKEOVER_DNS_PORT:-5300}
TAKEOVER_ADDRESS_FLAGS=${TAKEOVER_ADDRESS_FLAGS:-}
EOF
}

# Load existing .env (if any) so user-edited TAKEOVER_SUFFIXES survives
# the write_env rewrite. Only the variables we know about are exported;
# unknown lines are ignored.
load_env() {
  [ -f .env ] || return 0
  set -a
  # shellcheck source=/dev/null
  . ./.env
  set +a
}

# Charset-validate a single suffix label before it gets interpolated into
# a dnsmasq flag, a sed pattern, or a /etc/resolver path. Mirrors
# certgen.ValidateLabel: ^[a-z0-9][a-z0-9.-]*$, no `..`, no trailing `.`
# or `-`. Reject at the door rather than letting a bad value reach
# downstream commands.
valid_suffix() {
  # Set LC_ALL=C so character ranges match ASCII bytes, not locale collation.
  # In en_US.UTF-8, [a-z] includes uppercase letters — LC_ALL=C prevents that.
  ( LC_ALL=C
    case "$1" in
      ''|*[!a-z0-9.-]*)        exit 1;;
      [!a-z0-9]*)              exit 1;;
      *..*|*.|*-)              exit 1;;
    esac
    exit 0
  )
}

# --- Suffix-conflict probe -------------------------------------------------
# Prints the conflicting IP (and returns 0) if another resolver is already
# authoritative for the given suffix on this machine — currently this means
# OrbStack's mDNS, the only known source of conflict on macOS. Prints
# nothing (and returns 1) if there's no conflict.
# Called once per cleaned suffix from takeover_setup().
# Side-effect-free; never writes anything.
detect_suffix_conflict() {
  local suffix="$1"
  # Cheap detect: is OrbStack installed and running?
  if ! (pgrep -x OrbStack >/dev/null 2>&1 || (command -v orb >/dev/null 2>&1 && orb status >/dev/null 2>&1)); then
    return 1
  fi
  # Functional probe: ask the system resolver for a known OrbStack-ish name
  # under the suffix. Use 'nginx.<anything>.<suffix>' — if OrbStack is
  # actively serving that suffix it will reply with a non-loopback IP.
  local probe_name="nginx.probe.${suffix}"
  local result
  result=$(dscacheutil -q host -a name "$probe_name" 2>/dev/null || true)
  if printf '%s' "$result" | grep -Eq 'ip_address: [0-9]+\.[0-9]+\.[0-9]+\.[0-9]+'; then
    local ip
    ip=$(printf '%s' "$result" | grep 'ip_address:' | awk '{print $NF}' | head -1)
    # If the answer is 127.0.0.1 that's us (a previous resolver write); not a
    # "working OrbStack" signal. Only refuse when it's a routable container IP.
    case "$ip" in
      127.*) return 1 ;;
    esac
    printf '%s' "$ip"
    return 0
  fi
  return 1
}

# --- Takeover phase --------------------------------------------------------
# Activates only when TAKEOVER_SUFFIXES is non-empty. Synthesizes
# TAKEOVER_ADDRESS_FLAGS from the suffix list, manages /etc/resolver/<suffix>
# files, and persists the synthesized flags back to .env so docker compose
# interpolation picks them up.
takeover_setup() {
  [ -z "${TAKEOVER_SUFFIXES:-}" ] && return 0

  case "$OS" in
    Darwin) ;;
    *)
      printf 'ERROR: TAKEOVER_SUFFIXES is set but takeover mode is currently macOS-only.\n' >&2
      exit 1
      ;;
  esac

  : "${TAKEOVER_DNS_PORT:=5300}"

  # Parse + validate once, then sort+dedup into a normalized space-separated
  # list. The same list drives flag synthesis and resolver-file management,
  # so mismatches between dnsmasq config and /etc/resolver entries can't
  # sneak in. Sorting matches discoverer's parseTakeoverSuffixes so .env
  # diffs stay stable when operators reorder TAKEOVER_SUFFIXES.
  local raw s
  local cleaned=""
  local IFS=,
  for raw in $TAKEOVER_SUFFIXES; do
    s=$(printf '%s' "$raw" | awk '{$1=$1};1')
    [ -z "$s" ] && continue
    if ! valid_suffix "$s"; then
      printf 'ERROR: invalid takeover suffix %s\n' "$s" >&2
      exit 1
    fi
    cleaned="$cleaned$s"$'\n'
  done
  unset IFS
  cleaned=$(printf '%s' "$cleaned" | sort -u | tr '\n' ' ')
  cleaned=${cleaned% }

  # If cleaning produced an empty list, nothing to do.
  if [ -z "$cleaned" ]; then
    rm -f dnsmasq.conf
    return 0
  fi

  # Synthesize TAKEOVER_ADDRESS_FLAGS from the cleaned list.
  local flags=""
  for s in $cleaned; do
    flags="$flags --address=/$s/127.0.0.1"
  done
  flags=${flags# }
  export TAKEOVER_ADDRESS_FLAGS="$flags"

  # Persist back to .env so docker compose's variable interpolation sees it.
  # Quote the value: $flags contains spaces, and load_env sources .env via
  # `set -a; . ./.env`, which splits on whitespace for unquoted values.
  if grep -q '^TAKEOVER_ADDRESS_FLAGS=' .env 2>/dev/null; then
    sed -i.bak "s|^TAKEOVER_ADDRESS_FLAGS=.*|TAKEOVER_ADDRESS_FLAGS=\"$flags\"|" .env
    rm -f .env.bak
  else
    printf 'TAKEOVER_ADDRESS_FLAGS="%s"\n' "$flags" >> .env
  fi

  # DNS-only tailnet entry: resolve this machine's OWN tailnet subdomain to
  # loopback so setup.<machine>.<tailnet> and <svc>.<proj>.<machine>.<tailnet>
  # are reachable on THIS host. Scoped to <machine>.<tailnet> (never the whole
  # tailnet) so peer names keep resolving via Tailscale MagicDNS — a 4-label
  # /etc/resolver entry out-specifies Tailscale's 3-label scutil resolver.
  # These get dnsmasq address rules + a resolver file but are NOT added to
  # TAKEOVER_SUFFIXES: the discoverer already issues the *.<machine>.<tailnet>
  # cert SAN and routers, so listing it as a routing suffix would duplicate
  # them. Rides the takeover/dnsmasq sidecar, so it is active whenever
  # TAKEOVER_SUFFIXES is non-empty.
  local dns_only=""
  if [ -n "$MACHINE_NAME" ] && [ -n "$TAILNET_DOMAIN" ]; then
    if valid_suffix "$MACHINE_NAME.$TAILNET_DOMAIN"; then
      dns_only="$MACHINE_NAME.$TAILNET_DOMAIN"
    else
      printf 'WARNING: skipping tailnet DNS entry — invalid suffix %s.%s\n' \
        "$MACHINE_NAME" "$TAILNET_DOMAIN" >&2
    fi
  fi

  # Generate dnsmasq.conf for the mounted-conf mechanism. The dockurr/dnsmasq
  # image entrypoint ignores CLI args but honors /etc/dnsmasq.conf when
  # mounted. This file is mounted read-only by docker-compose.yml.
  # Fix: dockerdynomesh-atn — CLI args were discarded by the image entrypoint.
  {
    printf 'no-resolv\n'
    printf 'no-hosts\n'
    printf 'bind-interfaces\n'
    printf 'listen-address=0.0.0.0\n'
    printf 'port=53\n'
    for s in $cleaned $dns_only; do
      printf 'address=/%s/127.0.0.1\n' "$s"
    done
  } > dnsmasq.conf

  # Conflict gate: refuse to set up resolver files if OrbStack is actively
  # serving the suffix (two systems can't own the same name cleanly).
  # FORCE_TAKEOVER=1 bypasses this check.
  if [ "${FORCE_TAKEOVER:-}" = "1" ]; then
    printf 'WARNING: FORCE_TAKEOVER=1 set — skipping conflict probe.\n'
  else
    local conflict_ip
    for s in $cleaned; do
      conflict_ip=$(detect_suffix_conflict "$s" 2>/dev/null || true)
      if [ -n "$conflict_ip" ]; then
        printf 'ERROR: OrbStack is already serving *.%s (probe: nginx.probe.%s -> %s).\n' "$s" "$s" "$conflict_ip" >&2
        printf 'Installing a takeover for the same suffix would conflict with OrbStack'"'"'s\n' >&2
        printf 'mDNS — some lookups would land here and some at OrbStack, with no guarantee\n' >&2
        printf 'which. Pick a different suffix, or set FORCE_TAKEOVER=1 to proceed anyway.\n' >&2
        exit 1
      fi
    done
  fi

  # Manage /etc/resolver/<suffix> files. Prompts before creating or
  # overwriting; YES=1 skips prompts (for CI / scripts).
  local desired path current ans
  desired=$(printf 'nameserver 127.0.0.1\nport %s\n' "$TAKEOVER_DNS_PORT")
  for s in $cleaned $dns_only; do
    path="/etc/resolver/$s"
    if [ -f "$path" ]; then
      current=$(cat "$path")
      if [ "$current" = "$desired" ]; then
        printf '==> resolver file already current: %s\n' "$path"
        continue
      fi
      printf 'WARNING: %s exists with different content:\n' "$path" >&2
      printf '%s\n' "$current" | sed 's/^/    /' >&2
      if [ "${YES:-}" != "1" ]; then
        printf 'Overwrite %s? [y/N] ' "$path"
        read -r ans
        case "$ans" in
          y|Y|yes|YES) ;;
          *) printf 'skipping %s\n' "$path"; continue;;
        esac
      fi
    else
      if [ "${YES:-}" != "1" ]; then
        printf 'About to create %s. Continue? [y/N] ' "$path"
        read -r ans
        case "$ans" in
          y|Y|yes|YES) ;;
          *) printf 'skipping %s\n' "$path"; continue;;
        esac
      fi
    fi
    printf '==> writing %s (sudo)\n' "$path"
    printf 'nameserver 127.0.0.1\nport %s\n' "$TAKEOVER_DNS_PORT" | sudo tee "$path" >/dev/null
  done
}

write_override() {
  rm -f docker-compose.override.yml
  if [ -z "$TAILSCALE_IP" ]; then
    return
  fi
  # Tailscale interface bindings: Traefik handles all host ports. The
  # setup router (Host=setup.<machine>.<tailnet>) routes HTTP traffic to
  # the welcome service on port 80; service routers (HTTPS) live on 443.
  cat > docker-compose.override.yml <<EOF
services:
  traefik:
    ports:
      - "127.0.0.1:80:80"
      - "127.0.0.1:443:443"
      - "$TAILSCALE_IP:80:80"
      - "$TAILSCALE_IP:443:443"
EOF
}

write_setup_router() {
  rm -f traefik/dynamic/setup.yml
  if [ -z "$MACHINE_NAME" ] || [ -z "$TAILNET_DOMAIN" ]; then
    return
  fi
  cat > traefik/dynamic/setup.yml <<EOF
# Auto-generated by bootstrap.sh.
#
# Setup routes serve the welcome page (cert + install script) on BOTH
# entrypoints, so users land on it whether their browser auto-upgrades
# to HTTPS or not. On :443 the browser will show a cert warning until
# the user installs the CA — that's expected; the page is reachable
# through the warning click-through.
#
# Routing matrix:
#
#  :80  setup.<suffix> / setup.<m>.<t> / <m>.<t>  → welcome (HTTP, no warning)
#  :443 setup.<suffix> / setup.<m>.<t> / <m>.<t>  → welcome (HTTPS, warns until trust)
#  :80  any other host (bare IP, typo, etc.)      → 301 → http://setup.<suffix>/
#  :443 any other host                            → 301 → http://setup.<suffix>/
#
# The redirect target is the LOCAL setup host (setup.<suffix>, e.g.
# setup.docker.localhost): it resolves to loopback on every OS (RFC 6761)
# and is covered by the *.<suffix> cert SAN, so it's reachable with a valid
# cert with no DNS setup. The tailnet hostnames are also matched (for remote
# access) but are NOT used as the redirect target — setup.<machine>.<tailnet>
# is not resolvable under default MagicDNS. See BUGREPORT Bug 3.
#
# Service routers (containers exposed via discoverer) attach to entrypoint
# 'websecure' only with explicit Host() rules — they outrank the
# catchall HostRegexp via priority.
http:
  routers:
    setup-http:
      entryPoints: [web]
      rule: "Host(\`setup.${SUFFIX:-docker.localhost}\`) || Host(\`setup.$MACHINE_NAME.$TAILNET_DOMAIN\`) || Host(\`$MACHINE_NAME.$TAILNET_DOMAIN\`)"
      service: welcome
      priority: 100
    setup-https:
      entryPoints: [websecure]
      rule: "Host(\`setup.${SUFFIX:-docker.localhost}\`) || Host(\`setup.$MACHINE_NAME.$TAILNET_DOMAIN\`) || Host(\`$MACHINE_NAME.$TAILNET_DOMAIN\`)"
      service: welcome
      tls: {}
      priority: 100
    setup-catchall-http:
      entryPoints: [web]
      rule: "HostRegexp(\`.+\`)"
      service: welcome
      middlewares: [redirect-to-setup]
      priority: 1
    setup-catchall-https:
      entryPoints: [websecure]
      rule: "HostRegexp(\`.+\`)"
      service: welcome
      middlewares: [redirect-to-setup]
      tls: {}
      priority: 1
  middlewares:
    redirect-to-setup:
      redirectRegex:
        regex: "^.*$"
        replacement: "http://setup.${SUFFIX:-docker.localhost}/"
        permanent: true
  services:
    welcome:
      loadBalancer:
        servers:
          - url: "http://welcome:80"
EOF
}

# Tear down the takeover sidecar and resolver files. Does not unwind
# anything else — the rest of the stack stays running. Idempotent.
teardown_takeover() {
  load_env
  if [ -z "${TAKEOVER_SUFFIXES:-}" ]; then
    printf 'TAKEOVER_SUFFIXES is empty — nothing to tear down.\n'
    exit 0
  fi
  case "$OS" in
    Darwin) ;;
    *)
      printf 'teardown-takeover is macOS-only.\n' >&2
      exit 1
      ;;
  esac

  printf '==> Stopping dnsmasq sidecar\n'
  docker compose --profile takeover stop dnsmasq 2>/dev/null || true
  docker compose --profile takeover rm -f dnsmasq 2>/dev/null || true

  local raw s path ans cleaned=""
  local IFS=,
  for raw in $TAKEOVER_SUFFIXES; do
    s=$(printf '%s' "$raw" | awk '{$1=$1};1')
    [ -z "$s" ] && continue
    if ! valid_suffix "$s"; then
      printf 'ERROR: invalid takeover suffix %s\n' "$s" >&2
      exit 1
    fi
    cleaned="$cleaned$s"$'\n'
  done
  unset IFS
  cleaned=$(printf '%s' "$cleaned" | sort -u | tr '\n' ' ')
  cleaned=${cleaned% }

  # Mirror takeover_setup: also remove the DNS-only tailnet resolver file.
  local dns_only=""
  if [ -n "$MACHINE_NAME" ] && [ -n "$TAILNET_DOMAIN" ] && \
     valid_suffix "$MACHINE_NAME.$TAILNET_DOMAIN"; then
    dns_only="$MACHINE_NAME.$TAILNET_DOMAIN"
  fi

  for s in $cleaned $dns_only; do
    path="/etc/resolver/$s"
    if [ ! -f "$path" ]; then
      printf '==> not present: %s\n' "$path"
      continue
    fi
    if [ "${YES:-}" != "1" ]; then
      printf 'Remove %s? [y/N] ' "$path"
      read -r ans
      case "$ans" in
        y|Y|yes|YES) ;;
        *) printf 'skipping %s\n' "$path"; continue;;
      esac
    fi
    printf '==> removing %s (sudo)\n' "$path"
    sudo rm -f "$path"
  done

  # Remove the generated conf so a stale file doesn't get mounted on a later
  # non-takeover run.
  rm -f dnsmasq.conf

  printf '\n'
  printf 'Resolver files and dnsmasq removed. To stop emitting takeover SANs/routes:\n'
  printf '  - blank TAKEOVER_SUFFIXES in .env\n'
  printf '  - re-run ./bootstrap.sh up (next reissue will drop the extra SANs)\n'
}

# Report CA trust status. Called after cert export in main(). Detects whether
# the exported root-ca.pem is already in the host trust store and prints a
# friendly one-liner if so, or scoped install instructions if not.
report_ca_trust() {
  [ -f ./root-ca.pem ] || return 0
  local fp
  fp=$(openssl x509 -in root-ca.pem -noout -fingerprint -sha1 2>/dev/null | cut -d= -f2 | tr -d ':' || true)
  [ -z "$fp" ] && return 0

  case "$OS" in
    Darwin)
      if security find-certificate -a -Z /Library/Keychains/System.keychain 2>/dev/null | grep -qi "$fp"; then
        printf 'Root CA already trusted on this host (./root-ca.pem, SHA1 %s).\n' "$fp"
      else
        printf 'Root CA exported to ./root-ca.pem\n'
        printf 'Install on macOS with:\n'
        printf '  sudo security add-trusted-cert -d -r trustRoot -k /Library/Keychains/System.keychain root-ca.pem\n'
      fi
      ;;
    Linux)
      local dest=/usr/local/share/ca-certificates/dockerdynomesh.crt
      if [ -f "$dest" ] && cmp -s ./root-ca.pem "$dest"; then
        printf 'Root CA already trusted on this host (./root-ca.pem, SHA1 %s).\n' "$fp"
      else
        printf 'Root CA exported to ./root-ca.pem\n'
        printf 'Install on Ubuntu with:\n'
        printf '  sudo cp root-ca.pem /usr/local/share/ca-certificates/dockerdynomesh.crt && sudo update-ca-certificates\n'
      fi
      ;;
  esac
}

main() {
  install_prereq
  load_env
  discover_tailscale
  write_env
  write_override
  write_setup_router
  takeover_setup
  docker network inspect dynomesh-net >/dev/null 2>&1 || \
    docker network create dynomesh-net
  # The ca volume is declared external (see docker-compose.yml) so `down -v`
  # can never wipe the root CA. External volumes must pre-exist — create it
  # idempotently here, mirroring the dynomesh-net pattern above.
  docker volume inspect dockerdynomesh_ca >/dev/null 2>&1 || \
    docker volume create dockerdynomesh_ca
  local profile_args=()
  if [ -n "${TAKEOVER_SUFFIXES:-}" ]; then
    profile_args+=(--profile takeover)
  fi
  # bash 3.2 (macOS default) errors under `set -u` when expanding an empty
  # array as "${arr[@]}". The ${arr[@]+...} guard makes it expand to nothing.
  docker compose ${profile_args[@]+"${profile_args[@]}"} up -d --build
  printf 'Waiting for certgen to produce root cert...\n'
  for _ in $(seq 1 60); do
    if docker compose exec -T certgen test -f /shared/ca/rootCA.pem 2>/dev/null; then
      # Skip the docker cp round-trip when the local file already matches the
      # volume's CA (warm bootstrap). Compare fingerprints to avoid a full copy.
      local vol_fp local_fp
      vol_fp=$(docker compose exec -T certgen openssl x509 -in /shared/ca/rootCA.pem -noout -fingerprint -sha1 2>/dev/null | cut -d= -f2 | tr -d ':' || true)
      local_fp=""
      if [ -f ./root-ca.pem ]; then
        local_fp=$(openssl x509 -in ./root-ca.pem -noout -fingerprint -sha1 2>/dev/null | cut -d= -f2 | tr -d ':' || true)
      fi
      if [ -n "$vol_fp" ] && [ "$vol_fp" = "$local_fp" ]; then
        : # fingerprints match — skip export
      else
        docker compose cp certgen:/shared/ca/rootCA.pem ./root-ca.pem
      fi
      break
    fi
    sleep 1
  done
  if [ -f ./root-ca.pem ]; then
    report_ca_trust
    # Defense-in-depth: snapshot the root CA (cert + key) outside the repo on
    # every up, so even a forced `docker volume rm` is recoverable. The
    # external-volume declaration already blocks `down -v`; this covers the
    # harder wipe. Written under $HOME so private keys never land in the repo.
    ca_backup_dir="${HOME}/.dockerdynomesh-backups/ca"
    mkdir -p "$ca_backup_dir"
    if docker run --rm -v dockerdynomesh_ca:/src:ro -v "$ca_backup_dir":/backup \
        alpine tar czf /backup/rootCA-latest.tgz -C /src . 2>/dev/null; then
      printf 'Root CA backed up to %s/rootCA-latest.tgz\n' "$ca_backup_dir"
    fi
  else
    # shellcheck disable=SC2016 # backticks are intentional copy-paste literal
    printf 'Warning: did not see root cert after 60s; check `docker compose logs certgen`.\n' >&2
  fi
}

case "${1:-up}" in
  up)
    shift || true
    main "$@"
    ;;
  teardown-takeover)
    shift
    teardown_takeover "$@"
    exit 0
    ;;
  down)
    shift
    docker compose down "$@"
    exit 0
    ;;
  *)
    printf 'usage: %s [up|down|teardown-takeover]\n' "$0" >&2
    exit 1
    ;;
esac
