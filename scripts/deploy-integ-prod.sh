#!/usr/bin/env bash
set -euo pipefail

readonly PROJECT_ID="project-e8ef2daf-0520-4018-b9f"
readonly ZONE="asia-southeast1-b"
readonly INSTANCE="integ-prod"
readonly SERVICE="event-context"
readonly PROXY_SERVICE="event-context-proxy"
readonly REMOTE_ROOT="/opt/event-driven-context"
readonly REMOTE_DATA="/var/lib/event-driven-context"
readonly PORT="8401"
readonly RELEASE_ID="$(date -u +%Y%m%dT%H%M%SZ)-$(git rev-parse --short HEAD)"
readonly TEMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TEMP_DIR"' EXIT

if [[ -n "$(git status --porcelain)" ]]; then
  echo "refusing deployment from a dirty worktree" >&2
  exit 1
fi

make check
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o "$TEMP_DIR/edc-server" ./cmd/edc-server
cp deploy/production/context-api.service "$TEMP_DIR/$SERVICE.service"
cp deploy/production/context-api.caddy "$TEMP_DIR/$SERVICE.Caddyfile"
cp deploy/production/event-context-proxy.service "$TEMP_DIR/$PROXY_SERVICE.service"
cp deploy/production/context-service-admin "$TEMP_DIR/context-service-admin"
cp deploy/production/context-service-admin.sudoers "$TEMP_DIR/context-service-admin.sudoers"

gcloud compute ssh "$INSTANCE" --tunnel-through-iap --project "$PROJECT_ID" --zone "$ZONE" --command "install -d -m 0700 '/tmp/$SERVICE-$RELEASE_ID'"
gcloud compute scp --tunnel-through-iap --project "$PROJECT_ID" --zone "$ZONE" \
  "$TEMP_DIR/edc-server" "$TEMP_DIR/$SERVICE.service" "$TEMP_DIR/$SERVICE.Caddyfile" "$TEMP_DIR/$PROXY_SERVICE.service" "$TEMP_DIR/context-service-admin" "$TEMP_DIR/context-service-admin.sudoers" \
  "$INSTANCE:/tmp/$SERVICE-$RELEASE_ID/"

gcloud compute ssh "$INSTANCE" --tunnel-through-iap --project "$PROJECT_ID" --zone "$ZONE" --command "sudo -n bash -s -- '$RELEASE_ID' '$SERVICE' '$REMOTE_ROOT' '$REMOTE_DATA' '$PORT'" <<'REMOTE_SCRIPT'
set -euo pipefail
release_id="$1"
service="$2"
remote_root="$3"
remote_data="$4"
port="$5"
runtime_user="yycy"
shared_group="context-admins"
stage_dir="/tmp/${service}-${release_id}"
release_dir="${remote_root}/releases/${release_id}"
old_target=""

cleanup() { rm -rf "$stage_dir"; }
trap cleanup EXIT
if [[ ! -x "$stage_dir/edc-server" || ! -f "$stage_dir/$service.service" || ! -f "$stage_dir/$service.Caddyfile" || ! -f "$stage_dir/event-context-proxy.service" || ! -x "$stage_dir/context-service-admin" || ! -f "$stage_dir/context-service-admin.sudoers" ]]; then
  echo "incomplete staged release" >&2
  exit 1
fi

for account in "$runtime_user" songyy; do
  if ! id "$account" >/dev/null 2>&1; then
    echo "required account missing: $account" >&2
    exit 1
  fi
done
if ! getent group "$shared_group" >/dev/null; then
  groupadd --system "$shared_group"
fi
usermod -aG "$shared_group" "$runtime_user"
usermod -aG "$shared_group" songyy

systemctl stop "$service.service" || true
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
install -d -o "$runtime_user" -g "$shared_group" -m 2775 "$remote_root"
install -d -o "$runtime_user" -g "$shared_group" -m 2775 "$remote_root/releases"
chown -R "$runtime_user:$shared_group" "$remote_data" "$remote_root"
chmod -R g+rwX "$remote_data" "$remote_root"
find "$remote_data" "$remote_root" -type d -exec chmod g+s {} +
install -d -o "$runtime_user" -g "$shared_group" -m 2775 "$release_dir"
install -o "$runtime_user" -g "$shared_group" -m 0775 "$stage_dir/edc-server" "$release_dir/edc-server"
install -m 0644 "$stage_dir/$service.service" "/etc/systemd/system/$service.service"
install -m 0644 "$stage_dir/$service.Caddyfile" "/etc/caddy/$service.Caddyfile"
install -m 0644 "$stage_dir/event-context-proxy.service" "/etc/systemd/system/event-context-proxy.service"
install -m 0755 "$stage_dir/context-service-admin" /usr/local/sbin/context-service-admin
install -m 0440 "$stage_dir/context-service-admin.sudoers" /etc/sudoers.d/context-service-admin
visudo -cf /etc/sudoers.d/context-service-admin >/dev/null

if [[ -L "$remote_root/current" ]]; then old_target="$(readlink -f "$remote_root/current")"; fi
ln -sfn "$release_dir" "$remote_root/current"
caddy validate --config "/etc/caddy/$service.Caddyfile" --adapter caddyfile >/dev/null
systemctl daemon-reload
systemctl enable --now "$service.service"
systemctl enable --now event-context-proxy.service
systemctl reload event-context-proxy.service
healthy=""
for _ in $(seq 1 15); do
  if systemctl is-active --quiet "$service.service" && systemctl is-active --quiet event-context-proxy.service && curl --fail --silent --show-error "http://127.0.0.1:${port}/healthz" >/dev/null && curl --fail --silent --show-error "https://context-api.integ.life/healthz" >/dev/null; then
    healthy="yes"
    break
  fi
  sleep 1
done
if [[ -z "$healthy" ]]; then
  systemctl status "$service.service" --no-pager >&2 || true
  if [[ -n "$old_target" ]]; then ln -sfn "$old_target" "$remote_root/current"; systemctl restart "$service.service"; else systemctl disable --now "$service.service" || true; fi
  exit 1
fi
REMOTE_SCRIPT

echo "Deployed $SERVICE release $RELEASE_ID"
