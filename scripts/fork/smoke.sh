#!/usr/bin/env bash
# smoke.sh — дымовой прогон сборки против живого хаба: версии, жизненный цикл агента, сообщения,
# расписание, --project по слагу. Каждая проверка печатает PASS/FAIL; любой FAIL — код выхода 1.
#
#   scripts/fork/smoke.sh <hub-endpoint> [--expect <тег>] [--other-project <слаг>] [--container <профиль>] [--brokers a,b]
#
#   --expect          ожидаемая версия хаба и CLI (по умолчанию — версия CLI из `scion version`)
#   --other-project   слаг проекта на хабе, отличного от проекта текущего каталога, для проверки (д);
#                     без него (д) — SKIP
#   --container       прогнать жизненный цикл ещё и агентом в контейнере на этом профиле брокера
#                     (Apple container на macOS — профиль с runtime type: container; Docker/Podman — свои профили)
#   --brokers         брокеры, которые обязаны быть online (по умолчанию — все брокеры хаба)
#
# Окружение: SCION_BIN (по умолчанию scion из PATH); SCION_HUB_CONFIG — токен и проект хаба, его читает
# сам scion; SMOKE_PROFILE (tmux-профиль, по умолчанию tmux), SMOKE_TIMEOUT (секунд на каждое ожидание, 180).
#
# Запуск — из каталога проекта, в котором создаются агенты: его .scion/state.yaml сверяется до и после.
# Агенты — из шаблона smoke-templates/smoke-tmux (на любое сообщение отвечает pong); шаблон заливается
# на хаб заново (глобально; старые копии во всех областях удаляются) через временную копию в ~/.scion/templates. Агенты smoke-* удаляются в конце и при обрыве.
#
# KNOWN — известный дефект, на код выхода не влияет: первое сообщение свежему claude-агенту теряется,
# пока Claude Code показывает стартовый диалог; ping тогда повторяется один раз.
set -uo pipefail

HUB=${1:?использование: smoke.sh <hub-endpoint> [--expect <тег>] [--other-project <слаг>] [--container <профиль>] [--brokers a,b]}
shift
EXPECT="" OTHER="" CONTAINER="" BROKERS=""
while [[ $# -gt 0 ]]; do
  case $1 in
    --expect) EXPECT=$2; shift 2 ;;
    --other-project) OTHER=$2; shift 2 ;;
    --container) CONTAINER=$2; shift 2 ;;
    --brokers) BROKERS=$2; shift 2 ;;
    *) echo "неизвестный аргумент: $1" >&2; exit 2 ;;
  esac
done

SCION_BIN=${SCION_BIN:-scion}
PROFILE=${SMOKE_PROFILE:-tmux}
TIMEOUT=${SMOKE_TIMEOUT:-180}
HERE=$(cd "$(dirname "$0")" && pwd)
TEMPLATE_SRC=$HERE/smoke-templates/smoke-tmux
TEMPLATE=smoke-tmux
RUN=$$
FAILS=0
AGENTS=()

S() { "$SCION_BIN" --hub "$HUB" --non-interactive "$@"; }
pass() { echo "PASS  $*"; }
fail() { echo "FAIL  $*"; FAILS=$((FAILS + 1)); }
known() { echo "KNOWN $*"; }
skip() { echo "SKIP  $*"; }
check() { local name=$1; shift; if "$@"; then pass "$name"; else fail "$name"; fi; }
json() { python3 -c "import json,sys; d=json.load(sys.stdin); $1" 2>/dev/null; }

cleanup() {
  rm -rf "$HOME/.scion/templates/$TEMPLATE.smoke-$RUN"
  local a
  for a in "${AGENTS[@]}"; do S delete "$a" >/dev/null 2>&1 || true; done
  [[ -n $OTHER ]] && S --project "$OTHER" delete "smoke-$RUN-e" >/dev/null 2>&1 || true
}
trap cleanup EXIT

# The project state file of the current directory (nearest .scion/state.yaml upwards).
state_file() {
  local d=$PWD
  while [[ $d != / ]]; do
    [[ -f $d/.scion/state.yaml ]] && { echo "$d/.scion/state.yaml"; return; }
    d=$(dirname "$d")
  done
}
state_sum() { local f; f=$(state_file); [[ -n $f ]] && shasum "$f" | cut -d' ' -f1 || echo none; }

agent_field() { # agent field → value ("" when the agent is not listed)
  S list --format json 2>/dev/null | json "
a=[x for x in d if x.get('name')=='$1']
print(a[0].get('$2') or '' if a else '')"
}
wait_for() { # wait_for <agent> <field> <regex>
  local end=$((SECONDS + TIMEOUT))
  while ((SECONDS < end)); do
    [[ $(agent_field "$1" "$2") =~ $3 ]] && return 0
    sleep 5
  done
  return 1
}
schedule_fired() { # the event's status becomes fired, with no error
  local st end=$((SECONDS + TIMEOUT))
  [[ -n $1 ]] || return 1
  while ((SECONDS < end)); do
    st=$(S schedule get "$1" --format json 2>/dev/null | json "print(d.get('status',''), 'error' if d.get('error') else '')")
    [[ $st == "fired " ]] && return 0
    [[ $st == failed* ]] && return 1
    sleep 5
  done
  return 1
}
list_ok() { S list --format json 2>/dev/null | python3 -c 'import json,sys; json.load(sys.stdin)' 2>/dev/null; }
gone() {
  local end=$((SECONDS + TIMEOUT))
  while ((SECONDS < end)); do
    [[ -z $(S list --format json 2>/dev/null | json "print(' '.join(x.get('name','') for x in d))" | tr ' ' '\n' | grep -x "$1") ]] && return 0
    sleep 5
  done
  return 1
}

conv_id() { # the user's DM conversation with <agent>
  S conversation list --format json 2>/dev/null | json "
c=[x['id'] for x in d.get('conversations',[]) if (x.get('dmPeer') or {}).get('name')=='$1']
print(c[0] if c else '')"
}
count_msgs() { # count_msgs <agent> <text>: messages from the agent with exactly <text> in the DM
  local c; c=$(conv_id "$1")
  [[ -z $c ]] && { echo 0; return; }
  local n
  n=$(S conversation messages "conv:$c" --format json 2>/dev/null | json "
print(sum(1 for m in d.get('items',[]) if m.get('sender')=='agent:$1' and m.get('msg','').strip()=='$2'))" 2>/dev/null)
  echo "${n:-0}"
}
wait_count() { # wait_count <agent> <text> <more-than>
  local end=$((SECONDS + TIMEOUT))
  while ((SECONDS < end)); do
    (($(count_msgs "$1" "$2") > $3)) && return 0
    sleep 5
  done
  return 1
}

# ping <agent> <label> [first]: message ping, expect one more pong. With "first", a lost first
# message is retried once and reported as KNOWN.
ping() {
  local before; before=$(count_msgs "$1" pong)
  S message "@$1" ping >/dev/null 2>&1
  if wait_count "$1" pong "$before"; then pass "$2: ping → pong"; return; fi
  if [[ ${3:-} == first ]]; then
    known "$2: первое сообщение потеряно, повтор"
    S message "@$1" ping >/dev/null 2>&1
    wait_count "$1" pong "$before" && { pass "$2: ping → pong (повтор)"; return; }
  fi
  fail "$2: ping → pong"
}

push_template() {
  local tmp=$HOME/.scion/templates/$TEMPLATE.smoke-$RUN
  S templates delete "$TEMPLATE" --hub >/dev/null 2>&1 # every scope: a stale copy must not shadow the new one
  mkdir -p "$(dirname "$tmp")" && cp -R "$TEMPLATE_SRC" "$tmp" &&
    S --global templates push "$TEMPLATE.smoke-$RUN" --name "$TEMPLATE" >/dev/null 2>&1
  local rc=$?
  rm -rf "$tmp"
  return $rc
}

# lifecycle <agent> <profile> <label>: start → running → ping → suspend → resume → ping → delete.
lifecycle() {
  local a=$1 p=$2 l=$3 out
  AGENTS+=("$a")
  out=$(S -p "$p" start "$a" -t "$TEMPLATE" "start" 2>&1)
  check "$l: start" grep -q "started" <<<"$out"
  check "$l: нет 'Uploading workspace'" bash -c '! grep -q "Uploading workspace" <<<"$1"' _ "$out"
  check "$l: running" wait_for "$a" phase '^running$'
  wait_for "$a" activity '^completed$' >/dev/null
  ping "$a" "$l" first
  S suspend "$a" >/dev/null 2>&1
  check "$l: suspend" wait_for "$a" phase '^suspended$'
  S resume "$a" >/dev/null 2>&1
  check "$l: resume" wait_for "$a" phase '^running$'
  wait_for "$a" activity '^(completed|idle|waiting_for_input)$' >/dev/null
  ping "$a" "$l после resume" first
  S delete "$a" >/dev/null 2>&1
  check "$l: delete" gone "$a"
}

echo "smoke: хаб $HUB, CLI $SCION_BIN, профиль $PROFILE, каталог $PWD"

# (а) версии и брокеры
HEALTH=$(curl -fsS -m 10 "$HUB/healthz" 2>/dev/null || echo '{}')
HUBV=$(json "print(d.get('scionVersion',''))" <<<"$HEALTH")
CLIV=$("$SCION_BIN" version 2>/dev/null | tail -1)
[[ -z $EXPECT ]] && EXPECT=$(awk '{print $2}' <<<"$CLIV")
check "(а) /healthz scionVersion = $EXPECT (хаб: ${HUBV:-нет ответа})" test "$HUBV" = "$EXPECT"
check "(а) scion version = $EXPECT (CLI: $CLIV)" grep -q " $EXPECT " <<<"$CLIV "
BJSON=$(S hub brokers --format json 2>/dev/null || echo '[]')
if [[ -n $BROKERS ]]; then
  for b in ${BROKERS//,/ }; do
    st=$(json "print(next((x.get('status','') for x in d if x.get('name')=='$b' or x.get('slug')=='$b'),''))" <<<"$BJSON")
    check "(а) брокер $b online (${st:-нет})" test "$st" = online
  done
else
  NOTON=$(json "print(' '.join(x.get('name','?')+'='+x.get('status','') for x in d if x.get('status')!='online'))" <<<"$BJSON")
  NB=$(json "print(len(d))" <<<"$BJSON")
  check "(а) брокеры online: $NB, не online: ${NOTON:-нет}" test -z "$NOTON" -a "$NB" -gt 0
fi

STATE0=$(state_sum)
check "шаблон $TEMPLATE залит на хаб" push_template

# (б) жизненный цикл tmux-агента
lifecycle "smoke-$RUN-t" "$PROFILE" "(б) tmux"

# (в) два агента, расписание, list --json
A=smoke-$RUN-a B=smoke-$RUN-b
AGENTS+=("$A" "$B")
S -p "$PROFILE" start "$A" -t "$TEMPLATE" "start" >/dev/null 2>&1
S -p "$PROFILE" start "$B" -t "$TEMPLATE" "start" >/dev/null 2>&1
if wait_for "$A" activity '^completed$' && wait_for "$B" activity '^completed$'; then
  ping "$A" "(в) $A" first # also makes sure A reads messages before the relay
  S message "@$B" ping >/dev/null 2>&1 # same for B (its first message may be lost)
  before=$(count_msgs "$A" "relay-ok agent:$B")
  S message "@$A" "relay $B" >/dev/null 2>&1
  check "(в) $A пишет $B, $B отвечает (relay-ok)" wait_count "$A" "relay-ok agent:$B" "$before"
  ev=$(S schedule create --in 20s --agent "$A" --message ping 2>&1 | sed -n 's/^Scheduled event created: //p')
  check "(в) schedule: одноразовое событие сработало (${ev:-не создано})" schedule_fired "$ev"
else
  fail "(в) агенты $A и $B не стартовали"
fi
check "(в) scion list --format json читается" list_ok
S delete "$A" "$B" >/dev/null 2>&1

# (г) агент в контейнере
if [[ -n $CONTAINER ]]; then
  lifecycle "smoke-$RUN-c" "$CONTAINER" "(г) контейнер ($CONTAINER)"
else
  skip "(г) контейнер (без --container)"
fi

check "state.yaml проекта не изменился" test "$(state_sum)" = "$STATE0"

# (д) --project <слаг другого проекта> из каталога проекта
if [[ -n $OTHER ]]; then
  before=$(state_sum)
  out=$(S --project "$OTHER" -p "$PROFILE" start "smoke-$RUN-e" -t "$TEMPLATE" "start" 2>&1)
  check "(д) start --project $OTHER" grep -q "started" <<<"$out"
  check "(д) нет 'Uploading workspace'" bash -c '! grep -q "Uploading workspace" <<<"$1"' _ "$out"
  S --project "$OTHER" delete "smoke-$RUN-e" >/dev/null 2>&1
  check "(д) state.yaml каталога не изменился" test "$(state_sum)" = "$before"
else
  skip "(д) --project <слаг> (без --other-project)"
fi

echo
if ((FAILS)); then echo "smoke: FAIL ($FAILS)"; exit 1; fi
echo "smoke: PASS"
