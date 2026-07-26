# Tmux Runtime: Host Execution

## Overview

A host-execution runtime for Scion that runs each agent as a plain host process inside a `tmux` window — no containers, no images, no daemon. Sits alongside the existing container runtimes (`docker`, `podman`, `kubernetes`, `container`) and targets local interactive development on a single operator's machine.

Ships in two pieces, each adoptable independently:

1. A `TmuxRuntime` that implements the `Runtime` interface in terms of `tmux new-window`, `tmux list-windows`, etc.
2. An optional `sciontool init --tmuxruntime` wrapper that brings the production-grade lifecycle features (hooks, telemetry, hub heartbeat, supervisor) to the host-execution path.

## Motivation

- **Zero infrastructure.** No Docker/Podman daemon, no image registry, no Kubernetes cluster. Only `tmux` ≥ 3.0.
- **Native filesystem speed.** Host paths reach the agent at their real paths — no bind mounts or virtiofs in between.
- **Direct attach.** `scion attach` runs `tmux attach -t <id>` with no exec hop.

Container runtimes remain the right choice for filesystem isolation, CPU/memory limits, reproducible images, multi-tenant safety, or any GCP / Hub feature that requires `sciontool init` to bind privileged host resources (metadata server, sidecar services).

## Implementation Design

### 1. `TmuxRuntime`

`pkg/runtime/tmux.go` defines `TmuxRuntime` implementing the `Runtime` interface:

```go
type TmuxRuntime struct {
    Command   string // defaults to "tmux"
    Session   string // defaults to "scion"
    HomeMode  string // currently always "agent"
    Sciontool string
}
```

`Run` constructs `tmux new-window -d -t <session> -n <name> -c <workspace> -e KEY=VAL ... -P -F '#{window_id}' <harness>`. Window IDs (`@0`, `@12`) form the agent's "container ID", qualified as `<session>:<window_id>` (e.g. `scion:@5`).

Agent metadata that container runtimes store as Docker labels is mapped to tmux user-options at window scope (`@scion-label-<key>`, `@scion-annotation-<key>`, `@scion-workspace`). The `-w` flag is mandatory — without it tmux defaults to session scope and every `Run` would overwrite the previous agent's labels.

### 2. HOME mode

Only `home_mode: agent` is supported today. `HOME=<agentHome>` overrides for the whole tmux window; `SCION_AGENT_HOME`, `SCION_AGENT`, and `SCION_PROJECT` are exported alongside the harness's own env. Resolved env-secrets and Env passthrough land via `-e` flags. The field reserves space for follow-up modes that preserve the operator's `$HOME`.

### 3. Sciontool wrapper (`sciontool: auto`)

Container runtimes wrap each harness in `sciontool init -- <harness>` (the image `ENTRYPOINT`). On the host, the `--tmuxruntime` flag selects a host-friendly subset that skips:

- `setupHostUser` — already running as operator.
- `gitCloneWorkspace` — tmux runtime clones before invoking sciontool.
- `configureSharedWorkspaceGit` — operator git config already inherited.
- `writeEnvFile` (`~/.scion/scion-env`) — would clobber operator state.
- Apple-container Claude debug-dir workaround — apple-container-only.
- Sidecar services — host port collisions.
- GCP metadata server (`169.254.169.254`) — needs root.

Keeps: lifecycle hooks, OTLP telemetry, hub heartbeat + JWT refresh, GitHub App token rotation, `max-duration`/`max-turns`/`max-model-calls` enforcement, supervisor signal forwarding.

The supervisor config is forced to `UID=0/GID=0/Username=""/Rootless=false` in tmux mode so no credential drop is attempted and `HOME/USER/LOGNAME` are not rewritten.

### 4. Wrapper resolution (`sciontool` field)

The wrapper is opt-in per-runtime via `V1RuntimeConfig.Sciontool`:

| Value | Behavior |
|---|---|
| `""` (default) | No wrap. Harness runs directly. Existing tmux deployments unaffected. |
| `"auto"` | `exec.LookPath("sciontool")`. On miss, emit one-line stderr warning and fall back to direct exec. |
| `"<abs-path>"` | Used verbatim. |

Resolution happens in `factory.GetRuntime`; the runtime sees only an empty string or an absolute path. `buildNewWindowArgs` prepends `[r.Sciontool, "init", "--tmuxruntime", "--"]` to the harness argv when set.

## Integration

### Factory registration

`pkg/runtime/factory.go` registers `tmux` in the runtime type switch. Invalid `home_mode` values fail closed with `ErrorRuntime` rather than silently defaulting.

### Settings schema

`pkg/config/schemas/settings-v1.schema.json` declares the tmux-specific fields under `runtimeConfig.properties` so `scion config validate` and legacy → v1 migration both accept them:

```json
"session":   { "type": "string" },
"home_mode": { "type": "string", "enum": ["agent"] },
"sciontool": { "type": "string" }
```

The `home_mode` enum is constrained so typos fail at the schema layer rather than at `GetRuntime` time.

### Image registry bypass

`config.IsHostExecutionRuntime("tmux")` returns true so `RequireImageRegistry` is skipped — host execution has no images to pull.

### Volume mounts

`Volumes` on the `RunConfig` are silently dropped with a one-line stderr warning. Host execution shares the operator's filesystem, so bind mounts are redundant.

### File and variable secrets

`writeHostFileSecrets` materializes `file`-type secrets under `<agentHome>` (only `~/`-prefixed targets are honored; absolute paths are refused). `writeVariableSecrets` writes `variable`-type secrets to `<agentHome>/.scion/secrets.json`.

### Git workspace clone

`cloneWorkspaceIfRequested` clones hub-managed git workspaces before `tmux new-window`. The same logic runs inside `sciontool init` in container mode; in tmux mode the runtime handles it directly so the wrapper's `--tmuxruntime` branch can skip `gitCloneWorkspace`. `GITHUB_TOKEN` (if present) is woven into the URL via `https://oauth2:<token>@...`; `sanitizeGitOutput` redacts it before any error surface.

## Method notes

| Method | Approach |
|---|---|
| `Name()` | Return `"tmux"`. |
| `ExecUser()` | Return `""` — agent runs as the current host user. |
| `Run()` | `buildNewWindowArgs`, `writeHostFileSecrets` / `writeVariableSecrets`, `cloneWorkspaceIfRequested`, `ensureSession`, name-collision check, `tmux new-window`, `lockWindowName` (disable auto-rename), `applyMetadata`. |
| `Stop()` | Send `C-c` then `kill-window`. Idempotent. |
| `Delete()` | `kill-window`. Idempotent — treats missing window as success. |
| `List()` | `tmux list-windows -a` (all sessions), then `show-options -w -t <target>` per window. Filters non-scion windows (no `scion.name` label). |
| `GetLogs()` | `capture-pane -p -J -S - -E -` for full scrollback. |
| `Attach()` | `runInteractiveCommand("tmux", "attach", "-t", target)`. Takes over the terminal. |
| `Sync()` | No-op — host filesystem is already visible. |
| `Exec()` | A `tmux` subcommand is forwarded with `-t` rewritten to the agent's target; any other binary runs on the host with `cwd = pane_current_path`. |
| `GetWorkspacePath()` | `show-option -w -v -t <target> @scion-workspace`. |
| `ImageExists`/`PullImage` | Return success — no images. |

## Auth recovery (reset-auth / token expiry)

The broker's `resetAuth` handler (`pkg/runtimebroker/handlers.go`) is
container-centric and **cannot reach tmux agents**:

- The token-write script derives `TOKEN_DIR` via `getent passwd scion`; under
  tmux, `Exec` runs the command **host-side with the broker's own
  environment** (see Method notes), where `getent` is absent on macOS and the
  agent's HOME is unknown → it tries `/.scion` and fails on the read-only
  root.
- The follow-up signal `kill -USR2 1` targets PID 1, which on a macOS host is
  launchd — sciontool init is an ordinary host process, not PID 1.

The working recovery contract for tmux agents (since `abf8d051`):

- sciontool's token-refresh loop **re-reads the canonical token file when a
  refresh attempt is auth-rejected** and adopts a different still-valid token
  (`Client.adoptTokenFromFile`). Writing a fresh token to
  `<agentHome>/.scion/scion-token` is sufficient; the agent recovers within
  one retry (≤5 min backoff). `SIGUSR2` to the sciontool init process remains
  an optional accelerator (immediate re-read).

Facts for a future native tmux `reset-auth` implementation (prototyped and
shelved 2026-07-26 in favor of the file-adoption fallback):

- Agent home resolves host-side as
  `config.GetAgentHomePath(<scion.project_path window annotation>, <slug>)` —
  the same pattern `getLogs` uses.
- The tmux pane's `pane_pid` **is** the sciontool init process (tmux execs
  the window command argv directly), so it is a reliable signal target;
  verify the pid still maps to a sciontool process before signaling —
  SIGUSR2's default disposition terminates an unrelated reused pid.

Related fork fix: upstream PR #322 wired `reset-auth` only on the
agent-scoped route while the CLI client posts to the project-scoped one, so
`scion reset-auth` always 404'd; fixed in `3fcbc972` (upstreamable).

## Decisions summary

| # | Topic | Decision |
|---|---|---|
| 1 | **`home_mode`** | Only `agent` for the initial release. The constant + field reserve space for future modes. |
| 2 | **Sciontool wrapper default** | Off (`sciontool: ""`). Opt-in via `auto` or explicit path. |
| 3 | **`sciontool: auto` on miss** | Graceful fallback to direct exec with one-line stderr warning, never hard error. |
| 4 | **Invalid `home_mode`** | Fail closed at `GetRuntime` time with `ErrorRuntime`. |
| 5 | **Volumes** | Silently dropped with stderr warning. Host FS is already visible. |
| 6 | **File/variable secrets** | Materialize under `<agentHome>` — touches per-agent state, not operator HOME. |
