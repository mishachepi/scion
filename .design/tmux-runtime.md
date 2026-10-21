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
    Command        string            // defaults to "tmux"
    Session        string            // default_tmux_session; defaults to "scion"
    HomeMode       string            // home_mode; "agent" is the only accepted value
    PreStartScript string            // runs before each new-window: <agentHome> <operator-$HOME>
    Env            map[string]string // runtimes.<name>.env, exported into every window
    Sciontool      string            // "" or an absolute path, resolved by the factory
    StopTimeout    time.Duration     // how long Stop/Delete wait for the pane's init to exit
}
```

`Run` constructs `tmux new-window -d -t <session> -n <name> -c <workspace> -e KEY=VAL ... -P -F '#{window_id}' <harness>`. Window IDs (`@0`, `@12`) form the agent's "container ID", qualified as `<session>:<window_id>` (e.g. `scion:@5`).

Agent metadata that container runtimes store as Docker labels is mapped to tmux user-options at window scope (`@scion-label-<key>`, `@scion-annotation-<key>`, `@scion-workspace`). The `-w` flag is mandatory — without it tmux defaults to session scope and every `Run` would overwrite the previous agent's labels.

### 2. HOME

`home_mode: agent` is the only accepted value. `HOME=<agentHome>` overrides for the whole tmux window; `SCION_AGENT_HOME`, `SCION_AGENT`, and `SCION_PROJECT` are exported alongside the harness's own env. Resolved env-secrets and Env passthrough land via `-e` flags.

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

`pkg/runtime/factory.go` registers `tmux` in the runtime type switch.

### Settings schema

`pkg/config/schemas/settings-v1.schema.json` declares the tmux-specific fields under `runtimeConfig.properties` so `scion config validate` and legacy → v1 migration both accept them:

```json
"default_tmux_session": { "type": "string" },
"pre_start_script":     { "type": "string" },
"home_mode":            { "type": "string", "enum": ["agent"] },
"env":                  { "type": "object" },
"sciontool":            { "type": "string" }
```

### Image registry bypass

`config.IsHostExecutionRuntime("tmux")` returns true so `RequireImageRegistry` is skipped — host execution has no images to pull.

### Volume mounts

`Volumes` on the `RunConfig` are silently dropped with a one-line stderr warning. Host execution shares the operator's filesystem, so bind mounts are redundant.

### File and variable secrets

`writeHostFileSecrets` materializes `file`-type secrets under `<agentHome>` (only `~/`-prefixed targets are honored; absolute paths are refused). `writeVariableSecrets` writes `variable`-type secrets to `<agentHome>/.scion/secrets.json`.

### Git workspace clone

`cloneWorkspaceIfRequested` clones hub-managed git workspaces before `tmux new-window`. The same logic runs inside `sciontool init` in container mode; in tmux mode the runtime handles it directly so the wrapper's `--tmuxruntime` branch can skip `gitCloneWorkspace`. `GITHUB_TOKEN` (if present) is woven into the URL via `https://oauth2:<token>@...`; `sanitizeGitOutput` redacts it before any error surface.

### Optional runtime capabilities

The tmux runtime implements these optional runtime interfaces:

| Interface | Effect |
|---|---|
| `PerProfileInstancesRuntime` | The broker builds one manager per tmux profile, so profiles that differ in session, pre-start script or env do not share an instance. |
| `HarnessReadinessExemptRuntime` | Message delivery does not wait on the container readiness files, which do not exist on the host. |
| `HostWorkspaceRuntime` | A container-script provisioner is handed the host workspace instead of the container mount point. |
| `InitSignaler` (`pkg/runtime/init_signal.go`) | reset-auth signals the pane's `sciontool init` by PID instead of `kill -USR2 1`, which on the host would hit the host's init. |

## Method notes

| Method | Approach |
|---|---|
| `Name()` | Return `"tmux"`. |
| `ExecUser()` | Return `""` — agent runs as the current host user. |
| `Run()` | `buildNewWindowArgs`, `writeHostFileSecrets` / `writeVariableSecrets`, `cloneWorkspaceIfRequested`, `ensureSession`, name-collision check, `tmux new-window`, `lockWindowName` (disable auto-rename), `applyMetadata`. |
| `Stop()` | Send `C-c`, `kill-window`, then wait up to `StopTimeout` for the pane's init to exit. Idempotent. |
| `Delete()` | `kill-window` and the same wait. Idempotent — a missing window is success; a window labelled with another `scion.run_id` is left alone (`ErrRunMismatch`). |
| `List()` | `tmux list-windows -a` (all sessions), then `show-options -w -t <target>` per window. Filters non-scion windows (no `scion.name` label). |
| `GetLogs()` | `capture-pane -p -J -S - -E -` for full scrollback. |
| `Attach()` | `runInteractiveCommand("tmux", "attach", "-t", target)`. Takes over the terminal. |
| `Sync()` | No-op — host filesystem is already visible. |
| `Exec()` | A `tmux` subcommand is forwarded with `-t` rewritten to the agent's target; any other binary runs on the host with the agent's identity (`HOME` = agent home from `@scion-home`, `cwd` = workspace, `SCION_*` markers), falling back to `pane_current_path` for windows without `@scion-home`. |
| `GetWorkspacePath()` | `show-option -w -v -t <target> @scion-workspace`. |
| `ImageExists`/`PullImage` | Return success — no images. |

## Decisions summary

| # | Topic | Decision |
|---|---|---|
| 1 | **Sciontool wrapper default** | Off (`sciontool: ""`). Opt-in via `auto` or explicit path. |
| 2 | **`sciontool: auto` on miss** | Graceful fallback to direct exec with one-line stderr warning, never hard error. |
| 3 | **Volumes** | Silently dropped with stderr warning. Host FS is already visible. |
| 4 | **File/variable secrets** | Materialize under `<agentHome>` — touches per-agent state, not operator HOME. |
| 5 | **`home_mode`** | Only `agent`; any other value fails at `GetRuntime` with `ErrorRuntime`, never silently defaulted. |
