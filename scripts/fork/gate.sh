#!/usr/bin/env bash
# gate.sh — гейт перед слиянием в mch: сборка чистая, набор упавших тестов ⊆ набора упавших на main.
#
#   scripts/fork/gate.sh [--full] [ветка-или-коммит, по умолчанию dev]
#
# Список падений main кэшируется по коммиту main и версии Go в <git-common-dir>/fork-gate/:
# обычный прогон гоняет тесты только проверяемой ветки, тесты main — когда кэша для этой пары нет.
# --full гоняет обе ветки и перезаписывает кэш.
#
# Тесты идут с пустым HOME и без переменных SCION_* и TMUX*: без этого они читают ~/.scion оператора,
# создают настоящих агентов и, запущенные из сессии агента, видят его окружение.
# «Зелёный» прогон на macOS недостижим (часть тестов upstream красная на Mac сама по себе), поэтому
# критерий — отсутствие новых падений относительно main.
set -euo pipefail

FULL=
[[ ${1:-} == --full ]] && { FULL=1; shift; }
REF=${1:-dev}
REPO=$(git -C "$(dirname "$0")" rev-parse --show-toplevel)
TMP=$(mktemp -d)
cd "$REPO"
MAIN=$(git rev-parse main)
CACHE_DIR=$(git rev-parse --path-format=absolute --git-common-dir)/fork-gate
CACHE=$CACHE_DIR/main-$MAIN-$(go env GOVERSION).txt
A=$TMP/ref; B=$TMP/main
git worktree add -q --detach "$A" "$REF"
cleanup() { git worktree remove --force "$A" 2>/dev/null || true; git worktree remove --force "$B" 2>/dev/null || true; }
trap cleanup EXIT

# Упавший тест — строка «--- FAIL: Имя»; пакет, тесты которого не собрались, — «FAIL пакет [build failed]».
clean_env() { env | sed -nE 's/^((SCION_|TMUX)[A-Za-z0-9_]*)=.*/-u \1/p'; }
fails() { ( cd "$1" && env $(clean_env) HOME=$(mktemp -d) go test -tags no_sqlite ./... 2>&1 \
  | grep -E '^--- FAIL|^FAIL[[:space:]].*\[(build|setup) failed\]' | sed 's/ (.*//; s/[[:space:]]\{1,\}/ /g' | sort -u ) || true; }

echo "==> go build ./... ($REF)"
( cd "$A" && go build ./... ) || { echo "сборка $REF упала"; exit 1; }
echo "==> тесты $REF"; fails "$A" > "$TMP/fails-ref"
if [[ -n $FULL || ! -f $CACHE ]]; then
  echo "==> тесты main ($MAIN)"
  git worktree add -q --detach "$B" "$MAIN"
  fails "$B" > "$TMP/fails-main"
  mkdir -p "$CACHE_DIR" && cp "$TMP/fails-main" "$CACHE.tmp" && mv "$CACHE.tmp" "$CACHE"
else
  echo "==> падения main из кэша $CACHE"; cp "$CACHE" "$TMP/fails-main"
fi
NEW=$(comm -23 "$TMP/fails-ref" "$TMP/fails-main")
if [[ -n $NEW ]]; then
  echo "регрессии относительно main:"; echo "$NEW"; exit 1
fi
echo "гейт пройден: упавших на $REF — $(wc -l < "$TMP/fails-ref" | tr -d ' '), на main — $(wc -l < "$TMP/fails-main" | tr -d ' ')"
