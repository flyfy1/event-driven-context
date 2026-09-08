#!/usr/bin/env bash
set -euo pipefail

readonly PROJECT_ID="project-e8ef2daf-0520-4018-b9f"
readonly ZONE="asia-southeast1-b"
readonly INSTANCE="integ-prod"
readonly SERVICE="event-context"
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
cp deploy/production/context-api.caddy "$TEMP_DIR/$SERVICE.caddy"

gcloud compute ssh "$INSTANCE" --tunnel-through-iap --project "$PROJECT_ID" --zone "$ZONE" --command "install -d -m 0700 '/tmp/$SERVICE-$RELEASE_ID'"
gcloud compute scp --tunnel-through-iap --project "$PROJECT_ID" --zone "$ZONE" \
  "$TEMP_DIR/edc-server" "$TEMP_DIR/$SERVICE.service" "$TEMP_DIR/$SERVICE.caddy" \
  "$INSTANCE:/tmp/$SERVICE-$RELEASE_ID/"

gcloud compute ssh "$INSTANCE" --tunnel-through-iap --project "$PROJECT_ID" --zone "$ZONE" --command "sudo -n bash -s -- '$RELEASE_ID' '$SERVICE' '$REMOTE_ROOT' '$REMOTE_DATA' '$PORT'" <<'REMOTE_SCRIPT'
set -euo pipefail
release_id="$1"
service="$2"
remote_root="$3"
remote_data="$4"
port="$5"
stage_dir="/tmp/${service}-${release_id}"
release_dir="${remote_root}/releases/${release_id}"
old_target=""

cleanup() { rm -rf "$stage_dir"; }
trap cleanup EXIT
if [[ ! -x "$stage_dir/edc-server" || ! -f "$stage_dir/$service.service" || ! -f "$stage_dir/$service.caddy" ]]; then
  echo "incomplete staged release" >&2
  exit 1
fi

if ! id "$service" >/dev/null 2>&1; then
  useradd --system --home-dir "$remote_data" --shell /usr/sbin/nologin "$service"
fi
install -d -o "$service" -g "$service" -m 0700 "$remote_data"
install -d -m 0755 "$remote_root/releases"
install -d -m 0755 "$release_dir"
install -m 0755 "$stage_dir/edc-server" "$release_dir/edc-server"
install -m 0644 "$stage_dir/$service.service" "/etc/systemd/system/$service.service"

if [[ -L "$remote_root/current" ]]; then old_target="$(readlink -f "$remote_root/current")"; fi
ln -sfn "$release_dir" "$remote_root/current"
install -m 0644 "$stage_dir/$service.caddy" "/etc/caddy/sites-enabled/$service.caddy"

if ! caddy validate --config /etc/caddy/Caddyfile --adapter caddyfile; then
  rm -f "/etc/caddy/sites-enabled/$service.caddy"
  if [[ -n "$old_target" ]]; then ln -sfn "$old_target" "$remote_root/current"; else rm -f "$remote_root/current"; fi
  exit 1
fi
systemctl daemon-reload
systemctl enable --now "$service.service"
if ! systemctl is-active --quiet "$service.service" || ! curl --fail --silent --show-error "http://127.0.0.1:${port}/healthz" >/dev/null; then
  systemctl status "$service.service" --no-pager >&2 || true
  if [[ -n "$old_target" ]]; then ln -sfn "$old_target" "$remote_root/current"; systemctl restart "$service.service"; else systemctl disable --now "$service.service" || true; fi
  exit 1
fi
systemctl reload caddy
REMOTE_SCRIPT

echo "Deployed $SERVICE release $RELEASE_ID"
