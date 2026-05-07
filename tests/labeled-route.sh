#!/bin/bash
# Integration test: a container with traefik.* labels is routed by
# Traefik's docker provider, not by the discoverer. Verifies:
#  - HTTPS handshake succeeds (cert covers the project's wildcard).
#  - auto.yml does NOT contain a router for the labeled container.
#  - Bare containers continue to be routed (coexistence).
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
cd "$SCRIPT_DIR"

PROJECT=labeledproj
SERVICE=app
LABELED_CONTAINER=ddm-labeled-app
BARE_CONTAINER=ddm-labeled-bare
HOST_LABELED="${SERVICE}.${PROJECT}.docker.localhost"
HOST_BARE="bare.${PROJECT}.docker.localhost"
ROOT_PEM=/tmp/dynomesh-root-labeled.pem

cleanup() {
  docker rm -f "$LABELED_CONTAINER" "$BARE_CONTAINER" >/dev/null 2>&1 || true
  rm -f "$ROOT_PEM"
}
trap cleanup EXIT

if ! docker compose ps --status running --format json 2>/dev/null | grep -q '"Service"'; then
  printf 'stack not running; calling ./bootstrap.sh\n'
  ./bootstrap.sh
fi

docker rm -f "$LABELED_CONTAINER" "$BARE_CONTAINER" >/dev/null 2>&1 || true

# Labeled container — owned by Traefik's docker provider.
docker run -d --rm --name "$LABELED_CONTAINER" --network dynomesh-net \
  --label com.docker.compose.project="$PROJECT" \
  --label com.docker.compose.service="$SERVICE" \
  --label traefik.enable=true \
  --label "traefik.http.routers.${PROJECT}-${SERVICE}.rule=Host(\`${HOST_LABELED}\`)" \
  --label "traefik.http.routers.${PROJECT}-${SERVICE}.entrypoints=websecure" \
  --label "traefik.http.routers.${PROJECT}-${SERVICE}.tls=true" \
  --label "traefik.http.services.${PROJECT}-${SERVICE}.loadbalancer.server.port=80" \
  nginx:alpine >/dev/null

# Bare container — owned by discoverer.
docker run -d --rm --name "$BARE_CONTAINER" --network dynomesh-net \
  --label com.docker.compose.project="$PROJECT" \
  --label com.docker.compose.service=bare \
  nginx:alpine >/dev/null

docker compose cp certgen:/shared/ca/rootCA.pem "$ROOT_PEM"

# Poll for the labeled host. Discoverer needs to reissue cert for the
# new project; Traefik's docker provider needs to rebuild routers.
deadline=$((SECONDS + 30))
while (( SECONDS < deadline )); do
  if curl -sf --resolve "${HOST_LABELED}:443:127.0.0.1" \
      --cacert "$ROOT_PEM" "https://${HOST_LABELED}/" >/dev/null; then
    break
  fi
  sleep 1
done
curl -sf --resolve "${HOST_LABELED}:443:127.0.0.1" \
  --cacert "$ROOT_PEM" "https://${HOST_LABELED}/" >/dev/null \
  || { echo "FAIL: labeled host did not respond over HTTPS"; exit 1; }

# Bare host must also work (coexistence). Poll independently — discoverer
# may still be reissuing the cert for the new project's bare service.
bare_deadline=$((SECONDS + 30))
while (( SECONDS < bare_deadline )); do
  if curl -sf --resolve "${HOST_BARE}:443:127.0.0.1" \
      --cacert "$ROOT_PEM" "https://${HOST_BARE}/" >/dev/null; then
    break
  fi
  sleep 1
done
curl -sf --resolve "${HOST_BARE}:443:127.0.0.1" \
  --cacert "$ROOT_PEM" "https://${HOST_BARE}/" >/dev/null \
  || { echo "FAIL: bare host did not respond — coexistence regression"; exit 1; }

# Discoverer must NOT have emitted a router for the labeled container.
# Router names are derived from hostname: app.labeledproj → app-labeledproj.
if docker compose exec -T traefik cat /shared/dynamic/auto.yml | grep -q "${SERVICE}-${PROJECT}:"; then
  echo "FAIL: discoverer emitted a router for labeled container — label-skip broken"
  exit 1
fi

echo "PASS: labeled-route"
