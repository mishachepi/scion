---
title: The tmux Runtime (Host Execution)
description: Run scion agents as plain host processes inside a tmux window — no containers, no images.
---

The **tmux runtime** runs each agent as a plain host process inside its own `tmux` window. No container, no image, no daemon — just `tmux` itself.

Per-agent state (cwd, scion env vars, `HOME`) is isolated at the tmux window level; the host kernel, filesystem, and network are shared.

:::caution[Different from in-container tmux]
Every container runtime wraps each agent's harness in an in-container tmux session — that tmux is a shell wrapper *inside* the container (see [Interactive Sessions with Tmux](/scion/local/tmux/)).

The **tmux runtime** is something different: tmux IS the runtime layer, and the agent is a host process. Both can coexist on the same machine.
:::

## When to use it

Choose the tmux runtime for **zero infrastructure** (no Docker/Podman, no images), **native filesystem speed**, and **direct attach** (`scion attach` runs `tmux attach` with no exec hop).

Pick a container runtime instead when you need isolation between agents, CPU/memory limits, reproducible images, or multi-tenant safety.

## Quick start

Install tmux 3.0+ and add a profile to `~/.scion/settings.yaml`:

```yaml
active_profile: tmux-local

profiles:
  tmux-local:
    runtime: tmux

runtimes:
  tmux:
    type: tmux
```

Then:

```sh
scion start my-agent "review the auth module"
```

Or ad-hoc, without editing settings:

```sh
scion start my-agent "do thing" --profile tmux
```

`scion list`, `scion attach`, `scion message`, `scion stop`, `scion delete` all work unchanged. The `image_registry` requirement is skipped automatically — host execution has no images.

## Workspace resolution

The tmux runtime uses scion's [standard Workspace Resolution](/scion/local/workspace/): `--workspace` wins, then git worktree (in a git repo), then project root.

:::tip[Single-operator vaults: pass `--workspace`]
For solo setups where the project root is your live working tree (personal vaults, single-developer monorepos), the default git-worktree behavior is usually not what you want. Pass `--workspace` to mount the project root directly:

```sh
scion start core --workspace /Volumes/mch "review the inbox"
```
:::

## Multiple sessions

By default every agent lands in a single tmux session named `scion`. Split swarms via the `session:` field on the runtime:

```yaml
profiles:
  lsa:
    runtime: tmux-lsa
  code:
    runtime: tmux-code

runtimes:
  tmux-lsa:
    type: tmux
    session: lsa-vault
  tmux-code:
    type: tmux
    session: code-projects
```

Session names containing `:` or `.` are rejected — tmux uses them as target separators.

## HOME model

`HOME=<agentHome>` overrides for the whole tmux window. Each agent gets a fully isolated home — parity with container runtimes. Claude / Gemini / Codex read their config from `<agentHome>/.<tool>/` natively. `SCION_AGENT_HOME=<agentHome>` is also exported as the stable handle for hooks/scripts/sciontool.

This leaves `<agentHome>` empty of operator credentials. Run any auth-requiring tool's login inside the agent (`scion attach my-agent`, then `claude login`).

```yaml
runtimes:
  tmux:
    type: tmux
    home_mode: agent      # currently the only supported value
```

`home_mode` is reserved for future modes that preserve the operator's `$HOME` and OAuth/Keychain state; today only `agent` is supported.

## Auth

For Claude pick one of `api-key` (`ANTHROPIC_API_KEY`), `oauth-token` (`CLAUDE_CODE_OAUTH_TOKEN`), `auth-file`, `vertex-ai`, or `manual` (auth bootstrapped by the operator inside the agent). Gemini / Codex use `auth-file` — scion's auto-detect locates the credentials file under the operator's `$HOME` and copies it into `<agentHome>` at provisioning.

## Isolation model

The boundary is at the window level, not the session level.

| Layer | Isolated per agent | Shared across agents |
| :--- | :--- | :--- |
| **tmux window** | `cwd`, env vars, window id, user-options | session lifecycle |
| **scion provisioning** | `<agentHome>`, per-agent git worktree, `scion-agent.json` | `~/.scion/`, parent `.scion/agents/` |
| **host** | — | host kernel, filesystem, `/tmp`, network |

Compared to container runtimes:

| Aspect | Container runtime | tmux runtime |
| :--- | :--- | :--- |
| Per-agent unit | 1 container | 1 tmux window |
| Inside that unit | Inner tmux session as wrapper | Harness process directly |
| HOME | Per-agent isolated home | Per-agent `HOME=<agentHome>` |
| FS isolation | Container namespace + bind mounts | None — host filesystem reaches the agent |
| Network | Container network namespace | Host network |

`tmux kill-session -t scion` kills every agent at once — the analogue of the docker daemon going down. Documented; not a defect.

## Trade-offs

| Container runtimes give you... | tmux runtime gives you... |
| :--- | :--- |
| Filesystem isolation | Process-level separation only (different cwd, same UID, same host FS) |
| CPU/memory limits | None — agents inherit host resources |
| `image_registry` for reproducibility | Not required |
| `ExecUser`, network namespaces | N/A — host execution |
| `Volumes` bind mounts | Silently dropped with a stderr warning — host FS is already visible |

File-type and variable-type `ResolvedSecrets` ARE supported: files materialize under `<agentHome>` (only `~/`-prefixed targets), variables land in `<agentHome>/.scion/secrets.json`. Hub-managed git-clone projects are cloned by tmux runtime into `config.Workspace` before `tmux new-window`.

If any row on the left is load-bearing, pick a container runtime.

## Production-grade features (opt-in via `sciontool init --tmuxruntime`)

Container runtimes wrap the harness in `sciontool init -- <harness>` (the image `ENTRYPOINT`). The tmux runtime can do the same on the host — set `sciontool` on the runtime config and each tmux window launches as `sciontool init --tmuxruntime -- <harness>` instead of `<harness>` directly.

```yaml
runtimes:
  tmux:
    type: tmux
    sciontool: auto                # ← "" (off) | "auto" | "/abs/path/to/sciontool"
```

`auto` looks `sciontool` up on `PATH` at startup; if missing, the runtime emits a one-line stderr warning and falls back to running the harness directly. An explicit absolute path is taken verbatim.

`sciontool init` with `--tmuxruntime` skips the container-only steps (privilege drop, GCP metadata server, sidecar services, env-file injection, the Apple-container Claude-debug workaround, in-container git clone — the tmux runtime handles git clone itself) and keeps the host-friendly ones.

| Feature | Without wrapper | With `sciontool: auto` |
| :--- | :--- | :--- |
| OTLP telemetry forwarder (`localhost:4317`/`4318`) | ❌ | ✅ — runs locally on the host |
| Hub heartbeat / agent JWT auto-refresh | ❌ | ✅ — keeps long-running agents authenticated |
| GitHub App token rotation (`gh` wrapper) | ❌ | ✅ — refreshes static `GITHUB_TOKEN` near expiry |
| Lifecycle hooks (`pre-start`, `post-start`, `pre-stop`, `session-end`) at runtime layer | ❌ | ✅ — fires on each tmux window's harness lifecycle |
| `max-duration` / `max-turns` / `max-model-calls` limits | ❌ | ✅ — sciontool enforces locally |
| Signal forwarding with grace period | ❌ | ✅ — supervisor catches SIGTERM, waits `grace_period`, then SIGKILL |
| GCP metadata server emulation (`169.254.169.254` → Vertex AI / GCS) | ❌ | ❌ — binding privileged IP would need root + risks shadowing real GCE metadata |
| Sidecar services (`scion-services.yaml`) | ❌ | ❌ — would bind host ports, collision risk |

Build sciontool with `go build -o sciontool ./cmd/sciontool` and place it on `PATH` (`/usr/local/bin/sciontool` or similar) before flipping `sciontool: auto`. Without sciontool on PATH or an explicit path, leaving `sciontool: ""` (default) keeps the lean, no-dependency behavior.

The wrapper is per-runtime, not per-agent — every agent on the profile picks up the same setting.

### Where the wrapper looks for hooks

`sciontool init --tmuxruntime` resolves the agent's state directory from `$SCION_AGENT_HOME` (always exported by the tmux runtime). Lifecycle-hook discovery, telemetry artifacts, and `agent-info.json` all live there:

```
$SCION_AGENT_HOME/
├── .scion/
│   ├── hooks/              ← pre-start.d/, post-start.d/, pre-stop.d/, session-end.d/
│   ├── scion-services.yaml (ignored in tmux mode)
│   └── ...
└── agent-info.json
```

This is **per-agent**, not operator-wide. Treat the per-agent hooks dir as the canonical location and manage it from your template (`~/.scion/templates/<name>/`).

## Limits

- **Detaching tmux does not stop agents.** They only stop on `scion stop` / `scion delete` or harness exit.
- **`scion attach` takes over your terminal.** It runs `tmux attach -t <id>` directly; press your tmux prefix + `d` to detach.
- **No resource caps.** A misbehaving harness can consume host RAM/CPU without scion noticing.
