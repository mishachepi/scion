#!/usr/bin/env bash
# rollout.sh — раскатка выложенного релиза по узлам флота одной командой, после fork-release.sh … --publish.
#
#   scripts/fork/rollout.sh <тег> [--only hub,hub-broker,mac,local] [--dry-run]
#
#   hub         хаб на Linux-хосте: плагин TG (install во временный файл + mv, старый — .<прежний тег>), затем
#               плейбук scion-hub-rebuild.yaml (hub_binary, hub_commit, hub_version)
#   hub-broker  брокер на хосте хаба: рестарт user-юнита scion-broker (плейбук его не трогает), runtime-broker status
#   mac         брокер на удалённом Mac: scp darwin-arm64 + shasum, rm+cp в каталог бинарей, kickstart -k
#   local       брокер на этом Mac: rm+cp darwin-arm64 и linux-arm64 (каталог runtimes.<имя>.binaries
#               контейнерного рантайма), bootout + bootstrap + kickstart
#   --dry-run   напечатать команды, ничего не выполнять
#
# Порядок фиксирован: хаб раньше брокеров. Старые бинари остаются рядом как <имя>.<прежний тег>. В конце —
# /healthz по тегу, scion version на каждом узле и `hub brokers`: все брокеры online. Сбой останавливает раскатку.
#
# Хосты, пользователи, ключи и пути — из файла $SCION_FLEET_ENV (у оператора, в форк не попадает); переменные —
# scripts/fork/fleet.env.example. Релиз берётся из $SCION_STAGED/<тег>/.
set -euo pipefail
TAG=${1:?тег (mch-YYYY.MM.DD[.N])}; shift
ONLY="hub,hub-broker,mac,local" DRY=0
while (($#)); do
  case $1 in
    --only) ONLY=$2; shift 2 ;;
    --dry-run) DRY=1; shift ;;
    *) echo "неизвестный аргумент: $1" >&2; exit 2 ;;
  esac
done
[[ $TAG =~ ^mch-[0-9]{4}\.[0-9]{2}\.[0-9]{2}(\.[0-9]+)?$ ]] || { echo "тег не похож на mch-YYYY.MM.DD[.N]: $TAG" >&2; exit 2; }
: "${SCION_FLEET_ENV:?путь к файлу флота (пример — scripts/fork/fleet.env.example)}"
# shellcheck source=/dev/null
source "$SCION_FLEET_ENV"
for v in HUB_URL HUB_SSH HUB_BIN HUB_BROKER_HOME INFRA_DIR ANSIBLE_KEY MAC_SSH MAC_BIN LOCAL_BIN LOCAL_LINUX_BIN LOCAL_BROKER_PLIST BROKER_LABEL; do
  [[ -n ${!v:-} ]] || { echo "$SCION_FLEET_ENV: не задана $v" >&2; exit 2; }
done

REL=${SCION_STAGED:?каталог выложенных релизов}/$TAG
[[ -f $REL/SHA256SUMS ]] || { echo "нет релиза $REL (fork-release.sh … --publish выкладывает его только при PASS smoke)" >&2; exit 2; }
(cd "$REL" && shasum -a 256 -c SHA256SUMS >/dev/null) || { echo "$REL: SHA256SUMS не сходится" >&2; exit 1; }
COMMIT=$(git -C "$(dirname "$0")" rev-parse "$TAG^{commit}")

step() { echo; echo "==> $*"; }
run() { if ((DRY)); then printf '  %q' "$@"; echo; else "$@"; fi; }
remote() { local h=$1; shift; run ssh "$h" bash -s -- "$(printf '%q ' "$@")"; } # скрипт — со stdin
wants() { [[ ,$ONLY, == *,$1,* ]]; }

# Замена бинарей на Mac: rm + cp (новый inode; запись поверх запущенного Mach-O убивает процесс), старый — рядом
# как <имя>.<тег прежнего scion> (.prev, если его не запустить: linux-бинари на Mac). Аргументы: каталог
# назначения, каталог-источник, имена.
read -r -d '' SWAP <<'SH' || true
set -euo pipefail
dst=$1 src=$2; shift 2
mkdir -p "$dst"
old=$("$dst/scion" version 2>/dev/null | sed -nE 's/^scion ([^ ]+) \(commit.*/\1/p' | tail -1) || true
for f in "$@"; do
  [[ -e $dst/$f ]] && cp -p "$dst/$f" "$dst/$f.${old:-prev}"
  rm -f "$dst/$f" && cp "$src/$f" "$dst/$f" && chmod 0755 "$dst/$f"
done
SH
swap() { bash -s -- "$@" <<<"$SWAP"; }

if wants hub; then
  step "хаб: плагин telegram"
  run scp "$REL/linux-amd64/scion-plugin-telegram" "$HUB_SSH:/tmp/scion-plugin-telegram.$TAG"
  remote "$HUB_SSH" "$HUB_BIN" "$TAG" <<'SH'
set -euo pipefail
bin=$1 tag=$2 p=$1/scion-plugin-telegram
old=$("$bin/scion" version 2>/dev/null | sed -nE 's/^scion ([^ ]+) \(commit.*/\1/p' | tail -1)
[[ -f $p ]] && sudo cp -n "$p" "$p.${old:-prev}"
sudo install -m0755 "/tmp/scion-plugin-telegram.$tag" "$p.new" && sudo mv -f "$p.new" "$p"
rm -f "/tmp/scion-plugin-telegram.$tag"
SH
  step "хаб: плейбук scion-hub-rebuild.yaml"
  if ((DRY)); then
    echo "  (cd $INFRA_DIR && ansible-playbook playbooks/infra/scion-hub-rebuild.yaml --private-key $ANSIBLE_KEY -e hub_binary=$REL/linux-amd64/scion -e hub_commit=$COMMIT -e hub_version=$TAG)"
  else
    (cd "$INFRA_DIR" && ansible-playbook playbooks/infra/scion-hub-rebuild.yaml --private-key "$ANSIBLE_KEY" \
      -e "hub_binary=$REL/linux-amd64/scion" -e "hub_commit=$COMMIT" -e "hub_version=$TAG")
  fi
fi

if wants hub-broker; then
  step "брокер на хосте хаба: рестарт"
  remote "$HUB_SSH" "$HUB_BIN" "$HUB_BROKER_HOME" <<'SH'
set -euo pipefail
systemctl --user restart scion-broker
sleep 5
HOME=$2 SCION_CLI_MODE=human "$1/scion" runtime-broker status | grep -E 'Connected|Version' || true
SH
fi

if wants mac; then
  step "брокер $MAC_SSH"
  run ssh "$MAC_SSH" mkdir -p "/tmp/$TAG"
  run scp -r "$REL/darwin-arm64" "$REL/SHA256SUMS" "$MAC_SSH:/tmp/$TAG/"
  remote "$MAC_SSH" "$TAG" <<'SH'
set -euo pipefail
cd "/tmp/$1" && grep ' darwin-arm64/' SHA256SUMS | shasum -a 256 -c - >/dev/null
SH
  remote "$MAC_SSH" "$MAC_BIN" "/tmp/$TAG/darwin-arm64" scion sciontool <<<"$SWAP"
  remote "$MAC_SSH" "$BROKER_LABEL" "$TAG" <<'SH'
set -euo pipefail
launchctl kickstart -k "gui/$(id -u)/$1"
rm -rf "/tmp/$2"
SH
fi

if wants local; then
  step "локальный брокер"
  run swap "$LOCAL_BIN" "$REL/darwin-arm64" scion sciontool
  run swap "$LOCAL_LINUX_BIN" "$REL/linux-arm64" scion sciontool
  run launchctl bootout "gui/$(id -u)/$BROKER_LABEL" || true
  # bootstrap сразу после bootout может упасть с «Input/output error» (служба ещё выгружается) — повторить
  for _ in 1 2 3 4 5; do
    if run launchctl bootstrap "gui/$(id -u)" "$LOCAL_BROKER_PLIST"; then break; fi
    ((DRY)) && break
    sleep 2
  done
  run launchctl kickstart "gui/$(id -u)/$BROKER_LABEL"
fi

((DRY)) && exit 0
step "проверка"
FAILS=0
ok() { echo "PASS  $*"; }
bad() { echo "FAIL  $*"; FAILS=$((FAILS + 1)); }
sleep 10
v=$(curl -fsS -m 10 "$HUB_URL/healthz" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("scionVersion",""))' 2>/dev/null || true)
[[ $v == "$TAG" ]] && ok "хаб $HUB_URL: $v" || bad "хаб $HUB_URL: ${v:-нет ответа}, ждали $TAG"
nodever() { sed -nE 's/^scion ([^ ]+) \(commit.*/\1/p' <<<"$1" | tail -1; }
{ wants hub || wants hub-broker; } && { v=$(nodever "$(ssh "$HUB_SSH" "$HUB_BIN/scion" version 2>/dev/null)"); [[ $v == "$TAG" ]] && ok "хост хаба: scion $v" || bad "хост хаба: scion ${v:-?}"; }
wants mac && { v=$(nodever "$(ssh "$MAC_SSH" "$MAC_BIN/scion" version 2>/dev/null)"); [[ $v == "$TAG" ]] && ok "$MAC_SSH: scion $v" || bad "$MAC_SSH: scion ${v:-?}"; }
wants local && { v=$(nodever "$("$LOCAL_BIN/scion" version 2>/dev/null)"); [[ $v == "$TAG" ]] && ok "локальный: scion $v" || bad "локальный: scion ${v:-?}"; }
notonline=""
for _ in 1 2 3 4 5 6; do
  notonline=$(SCION_CLI_MODE=human "$LOCAL_BIN/scion" --hub "$HUB_URL" --non-interactive hub brokers --format json 2>/dev/null |
    python3 -c 'import json,sys; print(" ".join(b.get("name","?")+"="+b.get("status","") for b in json.load(sys.stdin) if b.get("status")!="online"))' 2>/dev/null || echo "нет ответа")
  [[ -z $notonline ]] && break
  sleep 10
done
[[ -z $notonline ]] && ok "брокеры хаба online" || bad "брокеры не online: $notonline"
echo
((FAILS)) && { echo "rollout: FAIL ($FAILS)"; exit 1; }
echo "rollout: PASS ($TAG)"
