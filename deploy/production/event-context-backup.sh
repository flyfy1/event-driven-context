#!/usr/bin/env bash
# Daily consistent backup for the Raspberry Pi deployment.
# The identity database and data directory are one consistency boundary, so the
# single writer is stopped briefly and always restarted, even on failure.
# The artifacts are handed to the host-level backup-sink (spool services/event-context,
# Google Drive, 30-day retention there); nothing is kept locally and unchanged content is
# not re-uploaded. See second-brain docs/261109-backup-sink.md.
set -euo pipefail

readonly SERVICE="event-context.service"
readonly STATE_DIR="/var/lib/event-driven-context"
readonly BACKUP_DIR="$STATE_DIR/backups"
readonly RUNTIME_OWNER="songyy:service-admins"
readonly SINK="${EDC_BACKUP_SINK:-/usr/local/bin/backup-sink}"
readonly SINK_PRODUCER="${EDC_BACKUP_SINK_PRODUCER:-services/event-context}"
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
# Content hashes while the writer is still stopped (the tarball itself is not deterministic).
db_hash="$(sha256sum "$DB_OUT" | cut -d' ' -f1)"
data_hash="$("$SINK" hash-tree "$STATE_DIR/data")"

restart_service
was_active=""

tar -tzf "$DATA_OUT" >/dev/null
chown "$RUNTIME_OWNER" "$DB_OUT" "$DATA_OUT"
chmod 0600 "$DB_OUT" "$DATA_OUT"

spooled=""
if "$SINK" unchanged "$SINK_PRODUCER" --label context --hash "$db_hash"; then
  rm -f "$DB_OUT"; echo "context.db unchanged since the last upload; not spooled"
else
  "$SINK" put "$SINK_PRODUCER" "$DB_OUT" --sha256 --content-hash "$db_hash" --label context; spooled="$spooled $DB_OUT"
fi
if "$SINK" unchanged "$SINK_PRODUCER" --label data --hash "$data_hash"; then
  rm -f "$DATA_OUT"; echo "data/ unchanged since the last upload; not spooled"
else
  "$SINK" put "$SINK_PRODUCER" "$DATA_OUT" --sha256 --content-hash "$data_hash" --label data; spooled="$spooled $DATA_OUT"
fi
echo "backup complete: spooled${spooled:- nothing} -> $SINK_PRODUCER"
