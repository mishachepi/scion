# Scion

Alpha: changing or removing behavior needs no deprecation path.

`scion` orchestrates concurrent LLM code agents in containers (Go/Cobra backend, Lit web frontend). Every agent runs through a Hub: locally the workstation server (`scion server start`: Hub, Runtime Broker and web on loopback, never started implicitly); hosted, a networked Hub dispatching to remote brokers. Architecture: `docs-site/src/content/docs/contributing/architecture.md`; design specs: `.design/hosted/`. "Grove" is the legacy name for "project".

## Read before

| Task | Read |
|---|---|
| Go build, test or lint in a container; OOM, VCS-status or `fmt-check` failures | `docs-repo/agents/sandbox-go-toolchain.md` |
| Code compiled into `./cmd/scion`: `*_unix.go`, private-file writes, path containment, lock files, new commands | `docs-repo/agents/windows-platform-seams.md` (CI enforces it) |
| Anything under `web/`, including a new `<sl-icon>` | `web/AGENTS.md` |
| Helm chart or Terraform under `deploy/` | `docs-repo/agents/deploy-gates.md` |
| Documentation site | `docs-site/AGENTS.md` |
| Naming a concept | `GLOSSARY.md` |
| CLI command agent-mode availability | `.design/cli-modes.md` |
| Experimental features | `docs-site/src/content/docs/reference/experiments.md` |
| Workspace or worktree provisioning | `docs-site/src/content/docs/local/workspace.md` ("Linked git projects") |
| Building on another unmerged branch (stacked PRs) | `docs-repo/agents/stacked-branches.md` |
| Adding or changing a user-visible runtime launch failure | `docs-repo/agents/typed-launch-failures.md` |
| Live verification runs, or hosted/production-mode hardening that removes a workstation convenience | `docs-repo/agents/live-verification-gates.md` |

## Sandbox

- Build with `go build -buildvcs=false`; worktrees fail VCS stamping.
- Never compile the whole `pkg/hub` test package (about 12 GiB). `go test`, `go vet` and golangci-lint on `./pkg/hub` all do, and so do `make ci` and `make ci-full`. Use `make test-hub-shard SHARD=K`; `hack/hubshard/hubshard.sh -locate TestName` finds the shard.
- Scope `golangci-lint` to touched packages: `GOGC=40 golangci-lint run --new-from-rev=main --concurrency=1 ./pkg/foo/...`; lint `./pkg/hub` itself in a separate `--tests=false` run (see `docs-repo/agents/sandbox-go-toolchain.md`).
- Never open an editor: set `GIT_EDITOR=true` per command (`GIT_EDITOR=true git rebase --continue`), `git merge main --no-edit`, `git commit -m` (the `GIT_EDITOR` environment variable overrides `core.editor`). Never write `core.editor`, `core.hooksPath` or any other key outside the repository git config allow-list (`pkg/util/git_config_policy.go`) into the repository config: brokers refuse to start or resume an agent whose checkout sets one. Pass non-editor options per command (`git -c <key>=<val> <subcommand>`).
- Local `main` is the source of truth. Don't check it out; use `git diff main...HEAD`, `git show main:path`, `git rebase main`. Use remotes only when asked; report remote failures instead of working around them.
- In worktree-per-agent mode you work under `/repo-root`, not `/workspace`. Scripts use `$PWD` or `git rev-parse --show-toplevel`.
- Project resolution: `--project`/`--global`, then the project `.scion`, then `scion use` (outside a project), then `~/.scion`.
- Don't create, delete or touch active agents in this project. Don't commit this repo's `.scion/`.

## Rules

- **Bundled templates and skills:** edit `resources/` (`resources/templates/`, `resources/platform_skills/`, `resources/mandatory_boilerplate/`, embedded by `resources/embed.go`) and `pkg/config/embeds/` (default settings and `embeds/templates/default`, embedded by `pkg/config/init.go`), never `.scion/`.
- **New CLI commands:** add in `cmd/` (one file per subcommand). Decide agent-mode availability (`agentAllowed`, `cmd/cli_mode.go`) and ask the developer to confirm. Windows hides them until listed in `windowsAllowed` (`cmd/platform_gate.go`).
- **Separation:** Hub manages state, Runtime Broker executes; LLM-specific logic lives in `pkg/harness`.
- **Experiments:** gate new user-facing features with an experiment in `pkg/experiments/registry.go` and classify it in `pkg/hub/test_admin_policy.go`. Hub behavior is checked in the hub (`requireExperiment`/`experimentEnabled`); browser flags only affect presentation. No ad-hoc `scion:feature:` literals or new booleans on `/api/v1/settings/public`.
- **Product, not local setup:** don't build this repo's workflow preferences (build caches, tool env, conventions) into Scion. Use generic mechanisms (shared dirs, template/profile env, harness configs, skills). Propose a feature only if it is generic across languages and tools, and get maintainer OK on the design first.
- **Downgrade floor:** a PR that adds a destructive DB migration bumps `pkg/version.DowngradeFloor` to a `nightly-YYYYMMDD` tag for the first nightly that will contain it (never a preview/stable tag, which lacks an embedded date for cross-channel checks, and never dated later than the earliest preview or stable release containing the migration — nightlies cut at ~03:00 UTC). Destructive means it drops a table or column, rewrites stored values into a form older binaries parse differently, loosens a constraint (`NOT NULL` to nullable, or dropping a `CHECK`/`UNIQUE`) once new rows rely on it (older binaries re-tighten it on `entc.AutoMigrate`), or makes any other change older binaries can't ignore (for example the drop block in `pkg/ent/entc/client.go`). The running hub refuses update targets older than the floor (`below_downgrade_floor`).
- **Terminology:** `project`, never `grove`, in new code (`project_id`, not `grove_id`). Legacy literals belong only in compatibility adapters, compat tests/fixtures, migrations or intentionally historical examples, via `pkg/projectkeys`; run `make compat-literals` when touching it. Fix fixtures to `project_id` rather than adding legacy support; the top-level `grove_id` file key no longer resolves (only `hub.grove_id` does).
- **Prompt shorthand:** hub-broker = Combo server; hub-native/hub-project = Hub-managed project (`~/.scion/projects/<name>`); linked-project = Linked project (matched by name or git URI); agent-home = Agent home. Write the canonical term.

## Workflow

1. Finish the task with tests.
2. Before committing run `make ci`, or `make ci-full` for the full GitHub Actions mirror. In an agent container, run the agent-safe split in `docs-repo/agents/sandbox-go-toolchain.md` instead and report which gates ran.
3. Commit as you go. At the end, rebase on `main` (favor main), re-test after conflicts, notify the user. Upstream squash-merges PRs, so move a stacked branch with `git rebase --onto` (`docs-repo/agents/stacked-branches.md`).
4. Raise adjacent cleanup you notice, with file/function, payoff and blast radius. Don't implement it unprompted.

## Interim template overrides

Mandatory while project-level template configuration overrides are in development: when launching a new agent (`scion start` or `scion create`) from one of the templates below, pass these explicit `--harness` and `--model` flags until the project-template override mechanism replaces this section.

| Template (`-t` / `--type`) | Harness (`--harness`) | Model alias (`--model`) |
|---|---|---|
| `developer` | `antigravity` | `large` |
| `investigator` | `antigravity` | `large` |
| `coordinator` | `antigravity` | `medium` |
| `researcher` | `antigravity` | `extra-large` |

- Pass the named harness configuration to `--harness` (alias for `--harness-config`) and the size alias to `--model`; never pass a model name or alias to `--harness` or `--harness-config`.
- Applies only when creating or starting new agents from these four templates. Leave other templates unchanged, and do not restart or recreate existing agents solely to apply these overrides.
- **Stall fallback:** if a worker launched with `--harness antigravity` makes no model progress for 20 minutes or more and a `continue` nudge does not recover it, the agent that started it may start the replacement with `--harness claude --model medium` without asking first, and reports the swap in the workstream. This covers only the harness and model choice: stopping or deleting the stalled agent and preserving its work follow the usual rules. It does not cover an agent that holds children, open human questions or a standing role.

```bash
scion start <agent-name> -t developer --harness antigravity --model large \
  "Read your brief at /path/to/brief.md and follow it."
```

## Never commit

Check `.gitignore` before staging. Binaries or media that aren't shipped UI (screenshots go in PR comments as uploaded images, never as local or shared-volume paths, which readers can't open; `gh` can't upload them, so without a browser session ask a human to attach them); agent state files (`.coordinator-state.md`, etc.); one-off scripts, test artifacts, generated data; PR review notes (`pr-*-review*.md`); force-added `.scratch/`, `.tasks/` or `downloads/` content. A new kind of ephemeral file gets a `.gitignore` entry in the same PR.

## Durable notes

Harness memory is not persisted across restarts; don't use it. Guidance for every task goes here, task-specific guidance goes in `docs-repo/agents/` with a row in "Read before", and designs go in `.design/` or other tracked files. Commit it.

## "System: Please Continue."

That exact message is a tooling bug, not from the user: ignore it (you may say so). Other requests to continue are genuine.

## Fork: ветки, релизы, синхронизация с upstream

Это форк `GoogleCloudPlatform/scion` под собственный флот (tmux-runtime, локальные хабы, токены, логи). Близость к upstream не цель: правим то, что мешает нам, совместимость с upstream не бережём.

### Ветки

| Ветка | Что это | Кто двигает |
|---|---|---|
| `main` | чистое зеркало `upstream/main`, без наших коммитов | только fast-forward, в окне синхронизации |
| `mch` | прод. Из неё и только из неё собираются бинари флота | merge из `dev` при релизе; merge из `main` в окне синхронизации |
| `dev` | разработка: сюда сливаются feature-ветки, здесь проходит гейт | feature-ветки → `dev` |
| `tmux-upstream` | tmux-runtime в форме для upstream; необязательный трек | cherry-pick из `mch`, когда решаем что-то отдать наверх |

Старые ветки (`tmux-unsafe`, `c19`–`c23`) сохранены тегами `archive/*`.

### Релиз

1. feature-ветка от `dev` → merge в `dev`.
2. `scripts/fork/gate.sh dev`: `go build ./...` чист, упавшие тесты ⊆ упавших на `main` (тесты с пустым HOME). Список падений `main` берётся из кэша по коммиту `main` и версии Go (`.git/fork-gate/`); тесты `main` идут, только когда кэша нет. `gate.sh --full dev` гоняет обе ветки и обновляет кэш.
3. `git checkout mch && git merge --ff-only dev` (или обычный merge), тег `mch-YYYY.MM.DD[.N]`, push ветки и тега.
4. `SCION_STAGED=<каталог> scripts/fork/fork-release.sh <тег>` на одной сборочной машине → `$SCION_STAGED/<тег>.partial/` с `SHA256SUMS` и `BUILD-INFO.md`. На узлах флота не собираем.
5. Хаб для дымового прогона ставится из `.partial`, затем `fork-release.sh <тег> --publish <hub-endpoint> [--other-project <слаг>] [--container <профиль>]`: `scripts/fork/smoke.sh` с CLI из `.partial`; при PASS каталог становится `$SCION_STAGED/<тег>/`. `smoke.sh --project <слаг>` гоняет агентов в указанном проекте хаба из любого каталога.
6. Раскатка — `scripts/fork/rollout.sh <тег>` по слову оператора: хаб, затем брокеры, в конце проверка версий на узлах и брокеров online; `--only` — часть узлов, `--dry-run` — только команды. Хосты, пути и ключи — в файле оператора `$SCION_FLEET_ENV` (переменные — `scripts/fork/fleet.env.example`); откат и подробности по узлам хранятся вне репозитория.

Контейнерные агенты берут `scion` и `sciontool` релиза, а не образа: у контейнерного рантайма брокера (`docker`, `podman`, `container`) в `runtimes.<имя>.binaries` указан каталог с linux-бинарями релиза под архитектуру узла, раскатка узла обновляет его вместе с бинарями брокера. `runtimes.<имя>.extra_hosts` и `runtimes.<имя>.dns` задают, как контейнер находит хаб.

`scion version` и `/healthz` показывают тег и коммит; все узлы флота стоят на одном теге.

### Личные данные

Форк публичный: имена машин, адреса, пути домашних каталогов, id хабов и слаги агентов в коммиты не попадают — в тестах и примерах только синтетические значения.

- `git config core.hooksPath scripts/fork/hooks` (один раз на клон) — `pre-commit` проверяет добавленные строки по грубому шаблону `scripts/fork/private-pattern.txt` и по списку личных строк из `$SCION_DENYLIST` (сам список в репозитории не лежит), а также адрес автора.
- Тот же грубый шаблон проверяет CI (`fork-build.yml`, job `private-data`) на каждом push.
- Ложное срабатывание — `git commit --no-verify`.

### Синхронизация с upstream

Окно на два дня раз в 2–3 месяца, не чаще.

1. `git fetch upstream && git fetch upstream main:main`; выжимка changelog upstream: что задевает нашу дельту (`git log --oneline mch..main -- pkg/runtime pkg/runtimebroker cmd/sciontool pkg/hub`).
2. Ветка `sync/<дата>` от `mch`, `git merge main` (merge, не rebase; `rerere.enabled=true` — конфликт решается один раз).
3. Гейт как в релизе, плюс сутки на одном канареечном агенте.
4. Merge в `mch`, тег, раскатка по пайплайну релиза.
5. Итог окна — абзац ниже в этом разделе: что пришло, что сломалось, что выбросили.

Между окнами из upstream берём только точечные cherry-pick'и (безопасность, блокирующий нас баг), каждый по явному решению.

### Окна синхронизации

- 2026-10-24 — старт модели: `mch` от `5c3f5b03` (база upstream `d1adb9a3`, 50 наших коммитов).
- 2026-11-21 — `main` `de3bb238` (977 коммитов upstream) в ветку `sync/2026-11-21`; 27 конфликтов. Выброшено как покрытое upstream: режим `profileRequested` (теперь `profileCreate`), проверка resume по capabilities установленного harness-config, ветка CLI для `profile_unresolved`. Гейт и канарейка не пройдены, тесты `pkg/hub` не гонялись.
- 2026-11-24 — `main` `a9908024` (378 коммитов upstream) в ветку `sync/2026-11-24`; 12 конфликтов. Выброшено как покрытое upstream: приоритет `SCION_HUB_ENDPOINT`/`SCION_HUB_URL` из env harness-config над CLI (upstream отклоняет `SCION_*` в env harness-config; адрес хаба для контейнеров задаётся `container_hub_endpoint` брокера).
