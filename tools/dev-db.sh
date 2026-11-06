#!/usr/bin/env sh
# Local PostgreSQL helper for OpenBekci development (Linux/macOS).
# Usage: tools/dev-db.sh init|start|stop|roles|url
# Env: PG_BIN (dir with initdb/pg_ctl/psql, default: PATH), OPENBEKCI_DEV_DIR, PGPORT.
set -eu

PG_BIN="${PG_BIN:-}"
[ -n "$PG_BIN" ] && PG_BIN="$PG_BIN/"
ROOT="${OPENBEKCI_DEV_DIR:-${XDG_DATA_HOME:-$HOME/.local/share}/OpenBekci}"
DATA_DIR="$ROOT/pgdata"
LOG_FILE="$ROOT/pg.log"
PORT="${PGPORT:-5432}"
ROLES_SQL="$(dirname "$0")/../deploy/compose/init/01-roles.sql"

ready() { "${PG_BIN}pg_isready" -h localhost -p "$PORT" -q; }

case "${1:-}" in
  init)
    if [ -f "$DATA_DIR/PG_VERSION" ]; then echo "Data dir exists: $DATA_DIR"; exit 0; fi
    mkdir -p "$ROOT"
    pw="$(mktemp)"; printf 'postgres' > "$pw"
    "${PG_BIN}initdb" -D "$DATA_DIR" -U postgres --pwfile="$pw" --auth=scram-sha-256 -E UTF8 --locale=C
    rm -f "$pw"
    ;;
  start)
    if ready; then echo "Already running."; exit 0; fi
    "${PG_BIN}pg_ctl" start -D "$DATA_DIR" -l "$LOG_FILE" -o "-p $PORT"
    i=0
    while [ $i -lt 60 ]; do
      if ready; then echo "PostgreSQL is ready."; exit 0; fi
      i=$((i+1)); sleep 0.5
    done
    echo "PostgreSQL did not become ready; see $LOG_FILE" >&2; exit 1
    ;;
  stop)
    "${PG_BIN}pg_ctl" stop -D "$DATA_DIR" -m fast
    ;;
  roles)
    PGPASSWORD="${PGSUPERPASSWORD:-postgres}" "${PG_BIN}psql" -h localhost -p "$PORT" -U postgres -d postgres -v ON_ERROR_STOP=1 -f "$ROLES_SQL"
    ;;
  url)
    echo "OPENBEKCI_DATABASE_URL=postgres://openbekci_app:openbekci@localhost:$PORT/openbekci"
    echo "OPENBEKCI_PLATFORM_DATABASE_URL=postgres://openbekci_platform:openbekci@localhost:$PORT/openbekci"
    echo "OPENBEKCI_MIGRATOR_DATABASE_URL=postgres://openbekci_migrator:openbekci@localhost:$PORT/openbekci"
    ;;
  *) echo "usage: $0 init|start|stop|roles|url" >&2; exit 2 ;;
esac
