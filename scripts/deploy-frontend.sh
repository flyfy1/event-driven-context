#!/usr/bin/env bash
set -euo pipefail

readonly REPOSITORY="flyfy1/event-driven-context"
readonly PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
readonly TEMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TEMP_DIR"' EXIT

cd "$PROJECT_ROOT"

if [[ -n "$(git status --porcelain)" ]]; then
  echo "refusing frontend deploy from a dirty worktree" >&2
  exit 1
fi
readonly SOURCE_REVISION="$(git rev-parse --short=12 HEAD)"
git clone --quiet --branch gh-pages "git@github.com:${REPOSITORY}.git" "$TEMP_DIR/site"
# The admin dashboard is published into the same Pages branch by its own
# repository. Keep that independently owned subtree when replacing this site.
rsync -a --delete --exclude '.git' --exclude '.DS_Store' --exclude 'admin/' frontend/ "$TEMP_DIR/site/"

# GitHub Pages serves JavaScript and CSS with long browser/CDN cache lifetimes.
# Give every local asset reference a release-specific URL so returning browsers
# cannot combine a fresh HTML document with code from an older deployment.
export FRONTEND_RELEASE="$SOURCE_REVISION"
while IFS= read -r -d '' html_file; do
  perl -0pi -e 's{((?:src|href)="\./[^"?]+\.(?:css|js))(?:\?v=[^"]*)?(")}{$1 . "?v=" . $ENV{FRONTEND_RELEASE} . $2}ge' "$html_file"
done < <(find "$TEMP_DIR/site" -path "$TEMP_DIR/site/admin" -prune -o -type f -name '*.html' -print0)

git -C "$TEMP_DIR/site" add --all
if git -C "$TEMP_DIR/site" diff --cached --quiet; then
  echo "Frontend already published"
  exit 0
fi
git -C "$TEMP_DIR/site" commit -m "Deploy frontend $(git rev-parse --short HEAD)"
git -C "$TEMP_DIR/site" push origin gh-pages
