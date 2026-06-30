#!/usr/bin/env bash
# Fetch upstream/<branch> and rebase the current branch onto it.
# Usage: scripts/sync-upstream.sh [upstream-branch]   (default: main)

set -euo pipefail

UPSTREAM_BRANCH="${1:-main}"

UPSTREAM_REMOTE=""
for candidate in upstream ustream; do
  if git remote get-url "$candidate" >/dev/null 2>&1; then
    UPSTREAM_REMOTE="$candidate"
    break
  fi
done
if [[ -z "$UPSTREAM_REMOTE" ]]; then
  echo "error: no 'upstream' or 'ustream' remote configured" >&2
  exit 1
fi

CURRENT_BRANCH="$(git symbolic-ref --short HEAD 2>/dev/null || true)"
if [[ -z "$CURRENT_BRANCH" ]]; then
  echo "error: detached HEAD; checkout a branch first" >&2
  exit 1
fi
if [[ "$CURRENT_BRANCH" == "$UPSTREAM_BRANCH" ]]; then
  echo "error: already on '$UPSTREAM_BRANCH'; checkout a feature branch first" >&2
  exit 1
fi

stashed=0
if ! git diff-index --quiet HEAD -- || [[ -n "$(git ls-files --others --exclude-standard)" ]]; then
  echo "stashing local changes..."
  git stash push -u -m "sync-upstream auto-stash $(date +%s)"
  stashed=1
fi

echo "fetching $UPSTREAM_REMOTE/$UPSTREAM_BRANCH..."
git fetch "$UPSTREAM_REMOTE" "$UPSTREAM_BRANCH"

echo "rebasing $CURRENT_BRANCH onto $UPSTREAM_REMOTE/$UPSTREAM_BRANCH..."
if ! GIT_EDITOR=true git rebase "$UPSTREAM_REMOTE/$UPSTREAM_BRANCH"; then
  cat >&2 <<EOF

rebase stopped with conflicts on $CURRENT_BRANCH.
resolve them in the listed files, then:
  git add <files>
  GIT_EDITOR=true git rebase --continue
or abort with:
  git rebase --abort
EOF
  if [[ "$stashed" -eq 1 ]]; then
    echo "your local changes are saved in 'git stash list' — pop them after the rebase finishes." >&2
  fi
  exit 1
fi

if [[ "$stashed" -eq 1 ]]; then
  echo "restoring local changes..."
  git stash pop
fi

echo "done. $CURRENT_BRANCH is now rebased on $UPSTREAM_REMOTE/$UPSTREAM_BRANCH."
