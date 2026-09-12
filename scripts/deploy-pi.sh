#!/usr/bin/env bash
set -euo pipefail

readonly TARGET="${EDC_DEPLOY_SSH_TARGET:-pi}"
readonly SERVICE="event-context"
readonly REMOTE_ROOT="/opt/event-driven-context"
readonly REMOTE_DATA="/var/lib/event-driven-context"
readonly PORT="8401"
readonly RELEASE_ID="$(date -u +%Y%m%dT%H%M%SZ)-$(git rev-parse --short HEAD)"
readonly TEMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TEMP_DIR"' EXIT

remote_exec() {
  ssh -o BatchMode=yes "$TARGET" "$1"
}

if [[ -n "$(git status --porcelain)" ]]; then
  echo "refusing deployment from a dirty worktree" >&2
  exit 1
fi

make check
BUILD_COMMIT="$(git rev-parse HEAD)"
BUILD_DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
BUILD_LDFLAGS="-s -w -X event-driven-context/internal/buildinfo.Commit=$BUILD_COMMIT -X event-driven-context/internal/buildinfo.BuiltAt=$BUILD_DATE"
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go -C backend build -trimpath -ldflags="$BUILD_LDFLAGS" -o "$TEMP_DIR/edc-server" ./cmd/edc-server
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go -C backend build -trimpath -ldflags="$BUILD_LDFLAGS" -o "$TEMP_DIR/edc" ./cmd/edc
cp -R backend/plugins "$TEMP_DIR/plugins"
cp -R backend/skills "$TEMP_DIR/skills"
cp deploy/production/context-api-pi.service "$TEMP_DIR/$SERVICE.service"

remote_exec "install -d -m 0700 '/tmp/$SERVICE-$RELEASE_ID'"
scp -r "$TEMP_DIR/edc-server" "$TEMP_DIR/edc" "$TEMP_DIR/plugins" "$TEMP_DIR/skills" "$TEMP_DIR/$SERVICE.service" "$TARGET:/tmp/$SERVICE-$RELEASE_ID/"

remote_exec "sudo -n bash -s -- '$RELEASE_ID' '$SERVICE' '$REMOTE_ROOT' '$REMOTE_DATA' '$PORT'" <<'REMOTE_SCRIPT'
set -euo pipefail
release_id="$1"
service="$2"
remote_root="$3"
remote_data="$4"
port="$5"
runtime_user="songyy"
shared_group="service-admins"
stage_dir="/tmp/${service}-${release_id}"
release_dir="${remote_root}/releases/${release_id}"
old_target=""

cleanup() { rm -rf "$stage_dir"; }
trap cleanup EXIT

if [[ ! -x "$stage_dir/edc" || ! -x "$stage_dir/edc-server" || ! -f "$stage_dir/$service.service" || ! -f "$stage_dir/plugins/project-brief/manifest.json" || ! -f "$stage_dir/skills/audio-transcribe/SKILL.md" || ! -f "$stage_dir/skills/daily-review/SKILL.md" ]]; then
  echo "incomplete staged release" >&2
  exit 1
fi
if [[ "$(uname -m)" != "aarch64" ]]; then
  echo "Raspberry Pi deployment requires an aarch64 target" >&2
  exit 1
fi
if ! id "$runtime_user" >/dev/null 2>&1 || ! getent group "$shared_group" >/dev/null; then
  echo "required Raspberry Pi runtime account or group is missing" >&2
  exit 1
fi
if [[ ! -f /etc/event-context.env ]]; then
  echo "missing /etc/event-context.env" >&2
  exit 1
fi

systemctl stop "$service.service" 2>/dev/null || true
install -d -o "$runtime_user" -g "$shared_group" -m 2770 "$remote_data"
backup_dir="$remote_data/backups"
install -d -o "$runtime_user" -g "$shared_group" -m 2770 "$backup_dir"
if [[ -f "$remote_data/context.db" ]]; then
  backup_path="$backup_dir/context-${release_id}.db"
  python3 -c 'import sqlite3, sys; source = sqlite3.connect(sys.argv[1]); target = sqlite3.connect(sys.argv[2]); source.backup(target); target.close(); source.close()' "$remote_data/context.db" "$backup_path"
  chown "$runtime_user:$shared_group" "$backup_path"
  chmod 0600 "$backup_path"
fi
if [[ -d "$remote_data/data" ]]; then
  data_backup="$backup_dir/data-${release_id}.tar.gz"
  tar -C "$remote_data" -czf "$data_backup" data
  chown "$runtime_user:$shared_group" "$data_backup"
  chmod 0600 "$data_backup"
fi

install -d -o "$runtime_user" -g "$shared_group" -m 2775 "$remote_root" "$remote_root/releases" "$release_dir"
install -o "$runtime_user" -g "$shared_group" -m 0775 "$stage_dir/edc-server" "$release_dir/edc-server"
install -o "$runtime_user" -g "$shared_group" -m 0775 "$stage_dir/edc" "$release_dir/edc"
cp -R "$stage_dir/plugins" "$stage_dir/skills" "$release_dir/"
chown -R "$runtime_user:$shared_group" "$release_dir"
find "$release_dir" -type d -exec chmod 2775 {} +
find "$release_dir/plugins" "$release_dir/skills" -type f -exec chmod 0644 {} +
install -m 0644 "$stage_dir/$service.service" "/etc/systemd/system/$service.service"

if [[ -L "$remote_root/current" ]]; then old_target="$(readlink -f "$remote_root/current")"; fi
ln -sfn "$release_dir" "$remote_root/current"
chown -h "$runtime_user:$shared_group" "$remote_root/current"
chown -R "$runtime_user:$shared_group" "$remote_data"
find "$remote_data" -type d -exec chmod g+s {} +
find "$backup_dir" -type f -exec chmod 0600 {} +
if [[ -f "$remote_data/notes-indexer.token" ]]; then chmod 0600 "$remote_data/notes-indexer.token"; fi

systemctl daemon-reload
systemctl enable --now "$service.service"
healthy=""
for _ in $(seq 1 20); do
  if systemctl is-active --quiet "$service.service" && curl --fail --silent --show-error "http://127.0.0.1:${port}/healthz" >/dev/null; then
    healthy="yes"
    break
  fi
  sleep 1
done
if [[ -z "$healthy" ]]; then
  systemctl status "$service.service" --no-pager >&2 || true
  if [[ -n "$old_target" ]]; then
    ln -sfn "$old_target" "$remote_root/current"
    systemctl restart "$service.service"
  else
    systemctl disable --now "$service.service" || true
  fi
  exit 1
fi

current_target="$(readlink -f "$remote_root/current")"
mapfile -t releases < <(find "$remote_root/releases" -mindepth 1 -maxdepth 1 -type d -printf '%T@ %p\n' | sort -nr | awk '{print $2}')
for index in "${!releases[@]}"; do
  candidate="${releases[$index]}"
  if [[ "$index" -lt 5 || "$candidate" == "$current_target" ]]; then
    continue
  fi
  case "$candidate" in
    "$remote_root/releases/"*) rm -rf -- "$candidate" ;;
    *) echo "refusing to prune unexpected release path: $candidate" >&2; exit 1 ;;
  esac
done
REMOTE_SCRIPT

echo "Deployed $SERVICE release $RELEASE_ID to $TARGET"
