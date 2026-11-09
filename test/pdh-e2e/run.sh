#!/usr/bin/env bash
# Ende-zu-Ende-Test PDH ↔ hmi-kiosk über TLS: echter Fernbefehl-Handler und HTTPS-Listener
# (internal/app/pdh_e2e_test.go, Build-Tag e2e) in einem Container, echter PDH-Client
# (pdh internal/web/kiosk_e2e_test.go, Build-Tag e2e) in einem zweiten, im selben Docker-Netz.
# Nur die Sitzungsverwaltung ist ein Skript. Räumt Container und Netz am Ende weg.
#
#   PDH_DIR=/c/workplace/pdh-server-kiosk bash test/pdh-e2e/run.sh
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
pdh="${PDH_DIR:?PDH_DIR auf das PDH-Repository setzen}"
case "$(uname -s)" in
  MINGW*|MSYS*|CYGWIN*) root="$(cygpath -m "$root")"; pdh="$(cygpath -m "$pdh")"; export MSYS_NO_PATHCONV=1 ;;
esac
net=hmik-pdh-e2e
srv=hmik-e2e
image=golang:1.24-bookworm

cleanup() {
  docker rm -f "$srv" >/dev/null 2>&1 || true
  docker network rm "$net" >/dev/null 2>&1 || true
}
trap cleanup EXIT
cleanup
docker network create "$net" >/dev/null

docker run -d --name "$srv" --network "$net" -e HMIK_E2E=1 \
  -v "$root:/src" -w /src \
  -v hmi-kiosk-gomod:/go/pkg/mod -v hmi-kiosk-gobuild:/root/.cache/go-build \
  "$image" go test -tags e2e -run TestPDHE2EServe -v -count=1 -timeout 0 ./internal/app/ >/dev/null

for _ in $(seq 1 180); do
  docker logs "$srv" 2>&1 | grep -q '^E2E_READY' && break
  if ! docker ps -q --filter "name=^${srv}$" | grep -q .; then
    docker logs "$srv"; echo "hmi-kiosk-Gegenstelle beendet"; exit 1
  fi
  sleep 1
done
logs="$(docker logs "$srv" 2>&1)"
key="$(sed -n 's/^E2E_KEY=//p' <<<"$logs")"
fp="$(sed -n 's/^E2E_FINGERPRINT=//p' <<<"$logs")"
[ -n "$key" ] && [ -n "$fp" ] || { echo "$logs"; echo "Schlüssel/Fingerabdruck fehlen"; exit 1; }
echo "Fingerabdruck (wie Admin-Seite): $fp"

status=0
docker run --rm --network "$net" \
  -e KIOSK_E2E_URL="https://$srv:8443" -e KIOSK_E2E_KEY="$key" -e KIOSK_E2E_FP="$fp" \
  -v "$pdh:/src" -w /src \
  -v pdh-kiosk-gomod:/go/pkg/mod -v pdh-kiosk-gobuild:/root/.cache/go-build \
  "$image" go test -tags e2e -run TestKioskE2E -v -count=1 ./internal/web/ || status=$?

echo "--- Protokoll hmi-kiosk (ohne Schlüssel) ---"
docker logs "$srv" 2>&1 | grep -v '^E2E_KEY=' | grep -E 'E2E_|Fernsteuerung' || true
exit "$status"
