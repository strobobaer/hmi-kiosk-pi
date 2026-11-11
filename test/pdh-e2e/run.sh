#!/usr/bin/env bash
# Ende-zu-Ende-Tests PDH ↔ hmi-kiosk über TLS, jeweils im selben Docker-Netz:
# 1. Fernbefehle: echter Handler und HTTPS-Listener von hmi-kiosk (internal/app/pdh_e2e_test.go,
#    Build-Tag e2e) in einem Container, echter PDH-Client (pdh internal/web/kiosk_e2e_test.go)
#    in einem zweiten. Nur die Sitzungsverwaltung ist ein Skript.
# 2. Terminal-Kopplung: echte PDH-Kopplungs-API und Geräteanmeldung auf frischer PostgreSQL
#    (pdh internal/web/terminal_pairing_e2e_test.go) in einem Container, echter Terminal-Client
#    (internal/product/pdhterminal/pdh_e2e_test.go): koppeln, anmelden, in PDH sperren.
# Räumt alle Container und das Netz am Ende weg.
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
pg=hmik-e2e-pg
pdhsrv=hmik-e2e-pdh
image=golang:1.24-bookworm

cleanup() {
  docker rm -f "$srv" "$pg" "$pdhsrv" >/dev/null 2>&1 || true
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
docker rm -f "$srv" >/dev/null 2>&1 || true
if [ "$status" -eq 0 ]; then echo "Szenario 1 (Fernbefehle): PASS"; else echo "Szenario 1 (Fernbefehle): FAIL"; fi

# --- Szenario 2: Terminal-Kopplung ---
docker run -d --name "$pg" --network "$net" -e POSTGRES_PASSWORD=pdh -e POSTGRES_DB=pdh_e2e postgres:16 >/dev/null
for _ in $(seq 1 60); do
  docker exec "$pg" pg_isready -U postgres -d pdh_e2e -h 127.0.0.1 >/dev/null 2>&1 && break
  sleep 1
done
docker exec "$pg" pg_isready -U postgres -d pdh_e2e -h 127.0.0.1 >/dev/null || { docker logs "$pg"; echo "PostgreSQL startet nicht"; exit 1; }

docker run -d --name "$pdhsrv" --network "$net" \
  -e "PDH_E2E_DSN=postgres://postgres:pdh@$pg:5432/pdh_e2e?sslmode=disable" -e "PDH_E2E_HOST=$pdhsrv" \
  -v "$pdh:/src" -w /src \
  -v pdh-kiosk-gomod:/go/pkg/mod -v pdh-kiosk-gobuild:/root/.cache/go-build \
  "$image" go test -tags e2e -run TestTerminalPairingE2EServe -v -count=1 -timeout 0 ./internal/web/ >/dev/null

for _ in $(seq 1 300); do
  docker logs "$pdhsrv" 2>&1 | grep -q '^E2E_READY' && break
  if ! docker ps -q --filter "name=^${pdhsrv}$" | grep -q .; then
    docker logs "$pdhsrv"; echo "PDH-Gegenstelle beendet"; exit 1
  fi
  sleep 1
done
plogs="$(docker logs "$pdhsrv" 2>&1)"
pcode="$(sed -n 's/^E2E_CODE=//p' <<<"$plogs" | tr -d '\r')"
pca="$(sed -n 's/^E2E_CA=//p' <<<"$plogs" | tr -d '\r')"
[ -n "$pcode" ] && [ -n "$pca" ] || { echo "$plogs"; echo "Code/Zertifikat fehlen"; exit 1; }

status2=0
docker run --rm --network "$net" \
  -e "HMIK_E2E_PDH_URL=https://$pdhsrv:8444" -e "HMIK_E2E_CODE=$pcode" -e "HMIK_E2E_CA=$pca" \
  -v "$root:/src" -w /src \
  -v hmi-kiosk-gomod:/go/pkg/mod -v hmi-kiosk-gobuild:/root/.cache/go-build \
  "$image" go test -tags e2e -run TestPDHKopplungE2E -v -count=1 ./internal/product/pdhterminal/ || status2=$?
if [ "$status2" -eq 0 ]; then echo "Szenario 2 (Kopplung): PASS"; else echo "Szenario 2 (Kopplung): FAIL"; fi

[ "$status" -eq 0 ] && [ "$status2" -eq 0 ]
