#!/bin/bash
# End-to-end smoke test: bring the stack up if needed, run a fresh sample
# container that creates a NEW compose project (so the discoverer must
# trigger certgen to reissue the wildcard with a new SAN), then verify
# HTTPS routing through Traefik with the local CA.
#
# Hard requirement: the curl --cacert handshake must succeed for this
# test to pass. Don't soften it.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
cd "$SCRIPT_DIR"

PROJECT=smoke
SERVICE=nginx
CONTAINER=ddm-smoke-nginx
HOST="${SERVICE}.${PROJECT}.docker.localhost"
ROOT_PEM=/tmp/dynomesh-root-smoke.pem

# shellcheck disable=SC2329 # invoked via the trap below
cleanup() {
  docker rm -f "$CONTAINER" >/dev/null 2>&1 || true
  rm -f "$ROOT_PEM"
}
trap cleanup EXIT

# Bring up the stack if not already running.
if ! docker compose ps --status running --format json 2>/dev/null | grep -q '"Service"'; then
  printf 'stack not running; calling ./bootstrap.sh\n'
  ./bootstrap.sh
fi

# Sample container on dynomesh-net. Compose labels make the discoverer
# treat this as project=smoke / service=nginx → host nginx.smoke.docker.localhost.
# Pre-clean: --rm only fires on graceful exit, so a hard-killed prior
# run can leave the name reserved.
docker rm -f "$CONTAINER" >/dev/null 2>&1 || true
docker run -d --rm --name "$CONTAINER" --network dynomesh-net \
  --label com.docker.compose.project="$PROJECT" \
  --label com.docker.compose.service="$SERVICE" \
  nginx:alpine >/dev/null

# Pull the root CA once.
docker compose cp certgen:/shared/ca/rootCA.pem "$ROOT_PEM"

# Poll until the new SAN propagates and the handshake succeeds.
# Discoverer is event-driven and certgen reissue takes ~1s; allow up
# to 30s before declaring failure.
TRAEFIK_HOST=127.0.0.1
deadline=$((SECONDS + 30))
result=1
while [ $SECONDS -lt $deadline ]; do
  if curl -fsS --resolve "${HOST}:443:${TRAEFIK_HOST}" \
       --cacert "$ROOT_PEM" "https://${HOST}/" 2>/dev/null \
     | grep -q "Welcome to nginx"; then
    result=0
    break
  fi
  sleep 1
done

# Verify the reissue path: start a container under a new project name,
# confirm auto.yml gains its route, and assert the cert fingerprint changed.
REISSUE_PROJECT=smoke-reissue
REISSUE_SERVICE=nginx
REISSUE_CONTAINER=ddm-smoke-reissue-nginx
REISSUE_HOST="${REISSUE_SERVICE}.${REISSUE_PROJECT}.docker.localhost"

docker rm -f "$REISSUE_CONTAINER" >/dev/null 2>&1 || true

# Fingerprint before the new project appears.
fp_before=$(echo | openssl s_client -connect 127.0.0.1:443 \
  -servername "$HOST" 2>/dev/null | openssl x509 -noout -fingerprint -sha1 2>/dev/null || true)

docker run -d --rm --name "$REISSUE_CONTAINER" --network dynomesh-net \
  --label com.docker.compose.project="$REISSUE_PROJECT" \
  --label com.docker.compose.service="$REISSUE_SERVICE" \
  nginx:alpine >/dev/null

# Wait for auto.yml to include the new project's route.
reissue_deadline=$((SECONDS + 30))
reissue_routed=0
while [ $SECONDS -lt $reissue_deadline ]; do
  if docker compose exec -T traefik cat /shared/dynamic/auto.yml 2>/dev/null \
       | grep -q "$REISSUE_HOST"; then
    reissue_routed=1
    break
  fi
  sleep 1
done

if [ $reissue_routed -eq 0 ]; then
  printf 'reissue smoke: auto.yml never gained route for %s\n' "$REISSUE_HOST" >&2
  docker rm -f "$REISSUE_CONTAINER" >/dev/null 2>&1 || true
  exit 1
fi

# Fingerprint after reissue.
fp_after=$(echo | openssl s_client -connect 127.0.0.1:443 \
  -servername "$HOST" 2>/dev/null | openssl x509 -noout -fingerprint -sha1 2>/dev/null || true)

docker rm -f "$REISSUE_CONTAINER" >/dev/null 2>&1 || true

if [ -z "$fp_before" ] || [ -z "$fp_after" ]; then
  printf 'reissue smoke: could not capture cert fingerprint (stack TLS not ready?)\n' >&2
  exit 1
fi
if [ "$fp_before" = "$fp_after" ]; then
  printf 'reissue smoke: cert fingerprint unchanged after new project — certgen did not reissue\n' >&2
  printf '  fingerprint: %s\n' "$fp_before" >&2
  exit 1
fi
printf 'reissue smoke passed: cert fingerprint changed after new project SAN\n'

# Optional takeover-mode smoke. Gated behind SMOKE_TAKEOVER=1 because it
# writes /etc/resolver and runs sudo. Uses foo.test (RFC 6761 reserved).
smoke_takeover() {
  [ "${SMOKE_TAKEOVER:-}" = "1" ] || { printf 'SMOKE_TAKEOVER not set; skipping takeover smoke\n'; return 0; }

  printf '==> takeover smoke: bringing stack up with TAKEOVER_SUFFIXES=foo.test\n'
  YES=1 TAKEOVER_SUFFIXES=foo.test TAKEOVER_DNS_PORT=5300 ./bootstrap.sh up

  printf '==> /etc/resolver/foo.test exists with expected content\n'
  grep -q '^nameserver 127.0.0.1$' /etc/resolver/foo.test
  grep -q '^port 5300$'           /etc/resolver/foo.test

  printf '==> dnsmasq container is running\n'
  docker compose --profile takeover ps dnsmasq | grep -Eq 'Up|running'

  printf '==> dnsmasq answers *.foo.test with 127.0.0.1\n'
  local answer
  answer=$(dig +short @127.0.0.1 -p 5300 nginx.smoke.foo.test || true)
  [ "$answer" = "127.0.0.1" ] || { printf 'expected 127.0.0.1, got %s\n' "$answer" >&2; return 1; }

  printf '==> https://nginx.smoke.foo.test/ returns 200\n'
  local code
  code=$(curl -sk --resolve nginx.smoke.foo.test:443:127.0.0.1 -o /dev/null -w '%{http_code}' \
              https://nginx.smoke.foo.test/)
  [ "$code" = "200" ] || { printf 'expected 200, got %s\n' "$code" >&2; return 1; }

  printf '==> teardown\n'
  YES=1 ./bootstrap.sh teardown-takeover
  [ ! -f /etc/resolver/foo.test ] || { printf 'teardown failed: /etc/resolver/foo.test still present\n' >&2; return 1; }

  printf 'PASS takeover smoke\n'
}

if [ $result -eq 0 ]; then
  printf '\xe2\x9c\x93 smoke test passed\n'
  smoke_takeover
else
  printf '\xe2\x9c\x97 smoke test failed\n' >&2
  printf -- '--- discoverer logs (tail) ---\n' >&2
  docker compose logs --tail 80 discoverer >&2 || true
  printf -- '--- certgen logs (tail) ---\n' >&2
  docker compose logs --tail 80 certgen >&2 || true
  printf -- '--- traefik dynamic config ---\n' >&2
  docker compose exec -T traefik cat /shared/dynamic/auto.yml >&2 || true
  printf -- '--- TLS handshake ---\n' >&2
  echo | openssl s_client -connect "${TRAEFIK_HOST}:443" -servername "$HOST" 2>&1 | head -40 >&2 || true
fi
exit $result
