#!/bin/bash
# Integration test: a container with traefik.* labels but NOT on
# dynomesh-net produces no route AND triggers the discoverer's
# remediation warning. Asserts:
#  - The remediation phrase appears in discoverer logs.
#  - The host is not reachable.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
cd "$SCRIPT_DIR"

PROJECT=misconfproj
SERVICE=app
CONTAINER=ddm-misconf-app
PRIVATE_NET=ddm-misconf-net
HOST="${SERVICE}.${PROJECT}.docker.localhost"
ROOT_PEM=/tmp/dynomesh-root-misconfig.pem

cleanup() {
  docker rm -f "$CONTAINER" >/dev/null 2>&1 || true
  docker network rm "$PRIVATE_NET" >/dev/null 2>&1 || true
  rm -f "$ROOT_PEM"
}
trap cleanup EXIT

if ! docker compose ps --status running --format json 2>/dev/null | grep -q '"Service"'; then
  printf 'stack not running; calling ./bootstrap.sh\n'
  ./bootstrap.sh
fi

docker network create "$PRIVATE_NET" >/dev/null 2>&1 || true

# Labeled container, but only on a private network (not dynomesh-net).
docker run -d --rm --name "$CONTAINER" --network "$PRIVATE_NET" \
  --label com.docker.compose.project="$PROJECT" \
  --label com.docker.compose.service="$SERVICE" \
  --label traefik.enable=true \
  --label "traefik.http.routers.${PROJECT}-${SERVICE}.rule=Host(\`${HOST}\`)" \
  nginx:alpine >/dev/null

# Wait briefly for the discoverer to reconcile.
deadline=$((SECONDS + 15))
matched=0
while (( SECONDS < deadline )); do
  if docker compose logs discoverer 2>&1 \
       | grep "container=${CONTAINER}\b" \
       | grep -q "not attached to dynomesh-net\. Traefik"; then
    matched=1
    break
  fi
  sleep 1
done
if (( matched == 0 )); then
  echo "FAIL: discoverer did not log remediation warning"
  docker compose logs discoverer 2>&1 | tail -30
  exit 1
fi

# Host should not be reachable (no route built). Use the project's root
# CA so we exercise the real wildcard cert path — -k would silently
# accept Traefik's default self-signed fallback. We expect a non-200
# (404 from the default backend, or a connect failure if Traefik isn't
# even serving the SNI). 200 means a route was unexpectedly built.
docker compose cp certgen:/shared/ca/rootCA.pem "$ROOT_PEM" >/dev/null
code=$(curl -s --max-time 3 --resolve "${HOST}:443:127.0.0.1" \
            --cacert "$ROOT_PEM" -o /dev/null -w '%{http_code}' \
            "https://${HOST}/" || true)
if [ "$code" = "200" ]; then
  echo "FAIL: host responded with 200 but no route should exist"
  exit 1
fi

echo "PASS: labeled-misconfig"
