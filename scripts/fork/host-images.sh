#!/usr/bin/env bash
# host-images.sh — образы агентов на Linux-хосте брокера из тега форка, воспроизводимо и с откатом.
#
#   scripts/fork/host-images.sh <тег> --host <user@host> [--ssh "<ssh-команда>"] [--steps "core-base scion-base harnesses"] [--activate]
#       на хосте: клон публичного репо по тегу в ~/scion-src/<тег> (https, без ключей), сборка штатным
#       image-build/scripts/build-images.sh (--builder local-docker, --registry local, --tag <тег>) по шагам,
#       проверка sciontool в scion-base; с --activate — local/<образ>:latest → <тег>.
#   scripts/fork/host-images.sh <тег> --host <user@host> --activate-only
#       только переключить :latest на уже собранный <тег>.
#   scripts/fork/host-images.sh <тег> --host <user@host> --rollback
#       вернуть :latest на состояние, записанное при активации <тег> (~/scion-src/latest-before-<тег>.txt).
#
# Образы остаются под своими тегами, :latest — единственное, что двигается. Журнал сборки: ~/scion-src/<тег>.build.log на хосте.
# Нужны на хосте: git, docker с buildx, доступ в GitHub по https. Брокер берёт образы как local/<имя>:latest (image_registry: local).
set -euo pipefail
TAG=${1:?тег (mch-YYYY.MM.DD[.N])}; shift
HOST="" SSH="ssh" STEPS="core-base scion-base harnesses" ACTIVATE=0 MODE=build
REPO_URL=${SCION_FORK_URL:-https://github.com/mishachepi/scion}
while (($#)); do
  case $1 in
    --host) HOST=$2; shift 2 ;;
    --ssh) SSH=$2; shift 2 ;;
    --steps) STEPS=$2; shift 2 ;;
    --activate) ACTIVATE=1; shift ;;
    --activate-only) MODE=activate; shift ;;
    --rollback) MODE=rollback; shift ;;
    *) echo "неизвестный аргумент: $1" >&2; exit 2 ;;
  esac
done
[[ -n $HOST ]] || { echo "--host <user@host> обязателен" >&2; exit 2; }
[[ $TAG =~ ^mch-[0-9]{4}\.[0-9]{2}\.[0-9]{2}(\.[0-9]+)?$ ]] || { echo "тег не похож на mch-YYYY.MM.DD[.N]: $TAG" >&2; exit 2; }

remote() { $SSH "$HOST" bash -s -- "$@"; }

case $MODE in
build)
  remote "$TAG" "$REPO_URL" "$STEPS" "$ACTIVATE" <<'REMOTE'
set -euo pipefail
TAG=$1 REPO_URL=$2 STEPS=$3 ACTIVATE=$4
SRC=~/scion-src/$TAG; LOG=~/scion-src/$TAG.build.log
mkdir -p ~/scion-src
if [[ ! -d $SRC/.git ]]; then
  git clone --quiet --depth 1 --branch "$TAG" "$REPO_URL" "$SRC"
fi
cd "$SRC"
[[ $(git describe --tags --exact-match 2>/dev/null) == "$TAG" ]] || { echo "в $SRC не тег $TAG" >&2; exit 1; }
echo "== $(date -Is) сборка $TAG: $STEPS (журнал $LOG)"
for step in $STEPS; do
  echo "== шаг $step"
  bash image-build/scripts/build-images.sh --builder local-docker --registry local --tag "$TAG" --target "$step" >>"$LOG" 2>&1 \
    || { echo "шаг $step упал, хвост журнала:"; tail -30 "$LOG"; exit 1; }
done
echo "== собрано:"; docker images --format '{{.Repository}}:{{.Tag}}\t{{.Size}}' | grep -F ":$TAG" | sort
echo "== sciontool в local/scion-base:$TAG:"; docker run --rm --entrypoint sciontool "local/scion-base:$TAG" version 2>/dev/null | head -2
if [[ $ACTIVATE == 1 ]]; then
  BEFORE=~/scion-src/latest-before-$TAG.txt; : >"$BEFORE"
  for ref in $(docker images --format '{{.Repository}}:{{.Tag}}' | grep -E "^local/scion-.*:$TAG$"); do
    name=${ref%:*}
    old=$(docker images --format '{{.Repository}}:{{.Tag}} {{.ID}}' | awk -v n="$name" '$1==n":latest"{print $2}')
    if [[ -n $old ]]; then oldtag=$(docker images --format '{{.Repository}}:{{.Tag}} {{.ID}}' | awk -v n="$name" -v id="$old" '$1!=n":latest" && index($1,n":")==1 && $2==id{sub(n":","",$1);print $1;exit}'); echo "$name ${oldtag:-$old}" >>"$BEFORE"; fi
    docker tag "$ref" "$name:latest"
  done
  echo "== :latest → $TAG (прежнее состояние: $BEFORE)"; cat "$BEFORE"
fi
REMOTE
  ;;
activate)
  remote "$TAG" <<'REMOTE'
set -euo pipefail; TAG=$1; BEFORE=~/scion-src/latest-before-$TAG.txt; : >"$BEFORE"
for ref in $(docker images --format '{{.Repository}}:{{.Tag}}' | grep -E "^local/scion-.*:$TAG$"); do
  name=${ref%:*}
  old=$(docker images --format '{{.Repository}}:{{.Tag}} {{.ID}}' | awk -v n="$name" '$1==n":latest"{print $2}')
  [[ -n $old ]] && echo "$name $old" >>"$BEFORE"
  docker tag "$ref" "$name:latest"
done
echo "== :latest → $TAG"; cat "$BEFORE"
REMOTE
  ;;
rollback)
  remote "$TAG" <<'REMOTE'
set -euo pipefail; TAG=$1; BEFORE=~/scion-src/latest-before-$TAG.txt
[[ -s $BEFORE ]] || { echo "нет $BEFORE — нечего откатывать" >&2; exit 1; }
while read -r name ref; do
  src=$ref; [[ $ref == *[^0-9a-f]* || ${#ref} -lt 12 ]] && src="$name:$ref"
  docker tag "$src" "$name:latest" && echo "$name:latest ← $src"
done <"$BEFORE"
REMOTE
  ;;
esac
