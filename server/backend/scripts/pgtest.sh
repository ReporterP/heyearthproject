#!/usr/bin/env bash
# pgtest.sh <package dir relative to server/backend> [test regex]
#
# Runs the Postgres-backed tests of one package inside a docker network, next to a throwaway
# PostgreSQL container — for machines where the host cannot reach ports docker publishes.
# The test binary is cross-compiled for Linux and run in a postgres:16-alpine container.
# The first run creates the network and the database container; its password is generated and
# lives only in that container.
#
#   bash scripts/pgtest.sh cmd/api '^TestFlagChanges'
#   bash scripts/pgtest.sh internal/biz            # every test of the package
#
# PGTEST_NETWORK and PGTEST_CONTAINER override the names.
set -eu
PKG="${1:?usage: pgtest.sh <package dir> [test regex]}"
RUN="${2:-.}"
# pwd -W: a Windows path under Git Bash (docker needs it for the volume), a no-op elsewhere.
BACKEND="$(cd "$(dirname "$0")/.." && (pwd -W 2>/dev/null || pwd))"
NET="${PGTEST_NETWORK:-hiearth-test}"
DBC="${PGTEST_CONTAINER:-hiearth-testpg2}"

docker network inspect "$NET" >/dev/null 2>&1 || docker network create "$NET" >/dev/null
if ! docker inspect "$DBC" >/dev/null 2>&1; then
  PW=$(head -c 24 /dev/urandom | base64 | tr -dc 'A-Za-z0-9' | head -c 20)
  docker run -d --name "$DBC" --network "$NET" -e POSTGRES_USER=hetest -e POSTGRES_PASSWORD="$PW" postgres:16-alpine >/dev/null
fi
docker start "$DBC" >/dev/null
for _ in $(seq 1 30); do
  docker exec "$DBC" pg_isready -U hetest >/dev/null 2>&1 && break
  sleep 1
done
PW=$(docker inspect "$DBC" --format '{{range .Config.Env}}{{println .}}{{end}}' | sed -n 's/^POSTGRES_PASSWORD=//p')

cd "$BACKEND"
OUT=.build/pgtest
mkdir -p "$OUT"
NAME=$(echo "$PKG" | tr '/.' '__')
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go test -c -o "$OUT/$NAME.test" "./$PKG"
MSYS_NO_PATHCONV=1 docker run --rm --network "$NET" \
  -v "$BACKEND:/src:ro" -v "$BACKEND/$OUT:/b:ro" -w "/src/$PKG" \
  -e TEST_MIGRATIONS_DIR=/src/migrations \
  -e TEST_DATABASE_URL="postgres://hetest:${PW}@${DBC}:5432/postgres?sslmode=disable" \
  postgres:16-alpine "/b/$NAME.test" -test.count=1 -test.v -test.run "$RUN" 2>&1 |
  grep -E "^(--- |PASS|FAIL|ok|panic|\s+[a-z_0-9]+_test\.go)" | tail -n 60
