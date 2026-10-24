#!/usr/bin/env bash
# gate.sh — гейт перед слиянием в mch: сборка чистая, набор упавших тестов ⊆ набора упавших на main.
#
#   scripts/fork/gate.sh [ветка-или-коммит, по умолчанию dev]
#
# Тесты идут с пустым HOME: без этого они читают ~/.scion оператора и создают настоящих агентов.
# «Зелёный» прогон на macOS недостижим (часть тестов upstream красная на Mac сама по себе), поэтому
# критерий — отсутствие новых падений относительно main.
set -euo pipefail

REF=${1:-dev}
REPO=$(git -C "$(dirname "$0")" rev-parse --show-toplevel)
TMP=$(mktemp -d)
cd "$REPO"
A=$TMP/ref; B=$TMP/main
git worktree add -q --detach "$A" "$REF"
git worktree add -q --detach "$B" main
cleanup() { git worktree remove --force "$A" 2>/dev/null || true; git worktree remove --force "$B" 2>/dev/null || true; }
trap cleanup EXIT

fails() { ( cd "$1" && HOME=$(mktemp -d) go test -tags no_sqlite ./... 2>&1 | grep -E '^--- FAIL' | sed 's/ (.*//' | sort -u ) || true; }

echo "==> go build ./... ($REF)"
( cd "$A" && go build ./... ) || { echo "сборка $REF упала"; exit 1; }
echo "==> тесты $REF"; fails "$A" > "$TMP/fails-ref"
echo "==> тесты main";  fails "$B" > "$TMP/fails-main"
NEW=$(comm -23 "$TMP/fails-ref" "$TMP/fails-main")
if [[ -n $NEW ]]; then
  echo "регрессии относительно main:"; echo "$NEW"; exit 1
fi
echo "гейт пройден: упавших на $REF — $(wc -l < "$TMP/fails-ref" | tr -d ' '), на main — $(wc -l < "$TMP/fails-main" | tr -d ' ')"
