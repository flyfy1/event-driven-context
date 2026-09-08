#!/usr/bin/env bash
set -euo pipefail

readonly REPOSITORY="flyfy1/event-driven-context"
readonly TEMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TEMP_DIR"' EXIT

if [[ -n "$(git status --porcelain)" ]]; then
  echo "refusing frontend deploy from a dirty worktree" >&2
  exit 1
fi
git clone --quiet --branch gh-pages "git@github.com:${REPOSITORY}.git" "$TEMP_DIR/site"
rsync -a --delete --exclude '.DS_Store' frontend/ "$TEMP_DIR/site/"
git -C "$TEMP_DIR/site" add --all
if git -C "$TEMP_DIR/site" diff --cached --quiet; then
  echo "Frontend already published"
  exit 0
fi
git -C "$TEMP_DIR/site" commit -m "Deploy frontend $(git rev-parse --short HEAD)"
git -C "$TEMP_DIR/site" push origin gh-pages
