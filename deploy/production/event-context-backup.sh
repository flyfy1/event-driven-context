#!/usr/bin/env bash
# Daily consistent backup for the Raspberry Pi deployment.
# The identity database and data directory are one consistency boundary, so the
# single writer is stopped briefly and always restarted, even on failure.
set -euo pipefail

readonly SERVICE="event-context.service"
readonly STATE_DIR="/var/lib/event-driven-context"
readonly BACKUP_DIR="$STATE_DIR/backups"
readonly RUNTIME_OWNER="songyy:service-admins"
readonly RETENTION_DAYS="${EDC_BACKUP_RETENTION_DAYS:-14}"
readonly STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
readonly DB_OUT="$BACKUP_DIR/daily-context-$STAMP.db"
readonly DATA_OUT="$BACKUP_DIR/daily-data-$STAMP.tar.gz"

was_active=""
restart_service() {
  if [[ -n "$was_active" ]]; then
    systemctl start "$SERVICE"
  fi
}
trap restart_service EXIT

install -d -o "${RUNTIME_OWNER%%:*}" -g "${RUNTIME_OWNER##*:}" -m 2770 "$BACKUP_DIR"
if systemctl is-active --quiet "$SERVICE"; then
  was_active="yes"
  systemctl stop "$SERVICE"
fi

python3 - "$STATE_DIR/context.db" "$DB_OUT" <<'PY'
import sqlite3, sys
source = sqlite3.connect(f"file:{sys.argv[1]}?mode=ro", uri=True)
target = sqlite3.connect(sys.argv[2])
source.backup(target)
source.close()
result = target.execute("PRAGMA integrity_check").fetchone()[0]
target.execute("PRAGMA journal_mode=DELETE")
target.close()
if result != "ok":
    sys.exit(f"integrity_check failed: {result}")
PY
tar -C "$STATE_DIR" -czf "$DATA_OUT" data

restart_service
was_active=""

tar -tzf "$DATA_OUT" >/dev/null
chown "$RUNTIME_OWNER" "$DB_OUT" "$DATA_OUT"
chmod 0600 "$DB_OUT" "$DATA_OUT"
(cd "$BACKUP_DIR" && sha256sum "$(basename "$DB_OUT")" "$(basename "$DATA_OUT")") >"$BACKUP_DIR/daily-$STAMP.sha256"
chmod 0600 "$BACKUP_DIR/daily-$STAMP.sha256"

find "$BACKUP_DIR" -maxdepth 1 -type f -name 'daily-*' -mtime "+$RETENTION_DAYS" -delete
echo "backup complete: $DB_OUT $DATA_OUT"
