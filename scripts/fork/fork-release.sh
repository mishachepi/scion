#!/usr/bin/env bash
# fork-release.sh — сборка релиза форка из тега mch-* в $SCION_STAGED/<тег>/.
#
#   SCION_STAGED=<каталог сборок> scripts/fork/fork-release.sh mch-2026.10.24
#       сборка в $SCION_STAGED/<тег>.partial/
#   SCION_STAGED=<каталог сборок> scripts/fork/fork-release.sh mch-2026.10.24 --publish <hub-endpoint> [аргументы smoke.sh]
#       scripts/fork/smoke.sh против хаба, уже поставленного из .partial, с CLI из .partial;
#       при PASS — выкладка: .partial → $SCION_STAGED/<тег>/
#
# Нужны go и node. Собирается только тег, лежащий на ветке mch.
# Результат: darwin-arm64/{scion,sciontool} — macOS-узлы (брокеры, локальные хабы);
#            linux-amd64/{scion,sciontool,scion-plugin-telegram} — Linux-хаб;
#            linux-arm64/{scion,sciontool} — агенты в arm64-Linux-контейнерах;
#            SHA256SUMS, BUILD-INFO.md.
# Сборочный worktree — <репо>__worktrees/release рядом с репозиторием (или $SCION_RELEASE_WORKTREE).
set -euo pipefail

TAG=${1:?тег вида mch-YYYY.MM.DD[.N]}
shift
STAGED=${SCION_STAGED:?задай SCION_STAGED — каталог, куда класть сборки}
REPO=$(git -C "$(dirname "$0")" rev-parse --show-toplevel)
REPO=$(git -C "$REPO" rev-parse --path-format=absolute --git-common-dir); REPO=${REPO%/.git}
WT=${SCION_RELEASE_WORKTREE:-${REPO}__worktrees/release}
die() { echo "ERROR: $*" >&2; exit 1; }

cd "$REPO"
git rev-parse -q --verify "refs/tags/$TAG^{commit}" >/dev/null || die "нет тега $TAG"
git merge-base --is-ancestor "$TAG" mch || die "$TAG не лежит на ветке mch"
SHA=$(git rev-parse "$TAG^{commit}"); SHORT=${SHA:0:8}
FINAL=$STAGED/$TAG
OUT=$FINAL.partial
[[ -e $FINAL ]] && die "$FINAL уже существует — релиз собирается один раз; для пересборки удали каталог"

if [[ ${1:-} == --publish ]]; then
  HUB=${2:?--publish <hub-endpoint>}; shift 2
  [[ -d $OUT ]] || die "нет $OUT — сначала сборка без --publish"
  ( cd "$OUT" && shasum -a 256 -c SHA256SUMS >/dev/null ) || die "SHA256SUMS в $OUT не сходятся"
  git -C "$WT" checkout -q --detach "$TAG"
  echo "==> smoke против $HUB"
  SCION_BIN=$OUT/darwin-arm64/scion "$WT/scripts/fork/smoke.sh" "$HUB" --expect "$TAG" "$@" || die "smoke не прошёл — релиз не выложен"
  mv "$OUT" "$FINAL"
  echo "выложено: $FINAL"
  exit 0
fi
[[ $# -eq 0 ]] || die "лишние аргументы: $*"
[[ -e $OUT ]] && die "$OUT уже существует — для пересборки удали каталог"

if [[ -d $WT ]]; then git -C "$WT" checkout -q --detach "$TAG"; else git worktree add -q --detach "$WT" "$TAG"; fi
cd "$WT"
LD=$(./hack/version.sh)
LDT=$(./hack/version.sh github.com/GoogleCloudPlatform/scion/cmd/sciontool/commands)
[[ $LD == *"Version=$TAG"* ]] || die "hack/version.sh не видит тег (ожидал Version=$TAG): $LD"

echo "==> make web"
make web

mkdir -p "$OUT/darwin-arm64" "$OUT/linux-amd64" "$OUT/linux-arm64"
export CGO_ENABLED=0 GOFLAGS=-mod=readonly
for oa in darwin-arm64 linux-amd64 linux-arm64; do
  echo "==> $oa"
  GOOS=${oa%-*} GOARCH=${oa#*-} go build -trimpath -buildvcs=false -ldflags "$LD"  -o "$OUT/$oa/scion"     ./cmd/scion
  GOOS=${oa%-*} GOARCH=${oa#*-} go build -trimpath -buildvcs=false -ldflags "$LDT" -o "$OUT/$oa/sciontool" ./cmd/sciontool
done
echo "==> scion-plugin-telegram (linux-amd64)"
( cd extras/scion-telegram && GOOS=linux GOARCH=amd64 go build -trimpath -buildvcs=false \
    -o "$OUT/linux-amd64/scion-plugin-telegram" ./cmd/scion-plugin-telegram )

V=$("$OUT/darwin-arm64/scion" version 2>/dev/null | tail -1)
[[ $V == *"$TAG"* && $V == *"$SHORT"* ]] || die "scion version → '$V', ожидал $TAG и $SHORT"

( cd "$OUT" && shasum -a 256 darwin-arm64/* linux-amd64/* linux-arm64/* > SHA256SUMS )
cat > "$OUT/BUILD-INFO.md" <<EOF
# $TAG

- commit: $SHA
- база upstream (merge-base с main): $(git merge-base "$TAG" main)
- собрано: $(date -u +%FT%TZ), $(go version | cut -d' ' -f3), CGO_ENABLED=0, web embedded
- darwin-arm64/{scion,sciontool} — macOS-узлы (брокеры, локальные хабы)
- linux-amd64/{scion,sciontool,scion-plugin-telegram} — Linux-хаб
- linux-arm64/{scion,sciontool} — агенты в arm64-Linux-контейнерах
- scion version: $V
EOF
echo "собрано: $OUT — поставь хаб для smoke из этой сборки и запусти: $0 $TAG --publish <hub-endpoint>"
cat "$OUT/BUILD-INFO.md"
