# Deprecating `claude-tmux` as a separate harness-config

Status: **draft — scoping only, no code changes yet**
Task: `[[deprecate-claude-tmux-harness-config]]` (epic: Scion Tmux - Upstream Runtime Contribution)
Directive: user, 2026-08-26 — remove the standalone `claude-tmux` harness-config; replace with a
runtime-aware overlay on the single `claude` config. Invariant: migration must not cause fleet
downtime, and needs a backward-compat window (M1/M4/M5 don't all migrate atomically).

## What exists today (verified against `~/.scion/harness-configs/` on M1/M5 + repo code, 2026-08-26)

Three installed configs implement Claude Code: `claude`, `claude-tmux`, `claude-tmux-skills`
(skills variant not yet diffed here — same shape expected, out of scope for this pass).

**`claude` vs `claude-tmux` — full diff, read line-by-line from both `config.yaml`:**

| Field | `claude` | `claude-tmux` |
|---|---|---|
| `provisioner.type` | `container-script` (runs `provision.py` in-container) | `builtin` |
| `command.base` args | `--dangerously-skip-permissions` | `--permission-mode bypassPermissions` |
| `command.resume_id_flag` | *(absent)* | `--resume {session_id}` — pins resume to the exact session id sciontool captured via `SessionStart` hook |
| `env.CLAUDE_CODE_ADDITIONAL_DIRECTORIES_CLAUDE_MD` | *(absent)* | `"1"` — makes Claude auto-load `CLAUDE.md`/`.claude/rules/` from the `--add-dir`'d agent home as memory, not just as a readable path |
| `capabilities.resume` | *(absent — no key at all)* | `{ support: "yes" }` |
| `auth.default_type` | `api-key` | `manual` |
| `auth.types` | `api-key`, `oauth-token`, `auth-file`, `vertex-ai` | `manual` (empty), `api-key` |

**Root cause confirmed, corrects a stale in-repo comment:** `claude-tmux/config.yaml`'s header
comment says "See `pkg/harness/claude_tmux.go`... `ClaudeTmux.ResolveAuth`" — **neither exists**
(`find . -iname claude_tmux.go` and `grep ClaudeTmux pkg/`, repo-wide: nothing). This matches
area-scion's finding: claude-tmux is pure config, not a distinct Go harness. The generic engine
(`pkg/harness/resolve.go::Resolve`) picks implementation purely from `entry.Provisioner != nil`
(→ `ContainerScriptHarness`) vs declarative metadata present (→ `DeclarativeGenericHarness`) —
no per-harness-name branching. Auth-type restriction ("claude-tmux only accepts manual/api-key")
is already 100% enforced by the generic auth engine (`pkg/harness/auth.go`) reading `auth.types`
from whichever config is loaded — nothing rejects other types in Go, the YAML's own `types:` map
*is* the rejection. **Action item:** fix the stale comment regardless of the overlay design below.

## Where a runtime-aware overlay could hook in

Both call sites of `harness.Resolve()` (`pkg/agent/run.go:418`, `pkg/agent/provision.go:832`)
have the resolved `Runtime` in scope *before* the call — confirmed at `run.go:359`,
`m.Runtime.Name()` is already read a few lines earlier for image-resolution logic. So
`ResolveOptions` could grow a `RuntimeName string` field, and `Resolve()` could apply a
tmux-specific overlay onto the `claude` entry when `RuntimeName == "tmux"`, instead of requiring
the caller/template to select a differently-named harness-config directory at all.

Sketch (not implemented): a new optional block in `HarnessConfigEntry`, e.g.
```yaml
runtime_overlays:
  tmux:
    provisioner: { type: builtin }
    command: { resume_id_flag: "--resume {session_id}" }
    env: { CLAUDE_CODE_ADDITIONAL_DIRECTORIES_CLAUDE_MD: "1" }
    capabilities: { resume: { support: "yes" } }
    auth: { default_type: manual, types: { manual: {}, api-key: {...} } }
```
merged over the base entry in `Resolve()` when the runtime matches, same merge function shape as
`mergeHarnessConfigEntries` (already exists for the settings overlay, `resolve.go:142`).

## Risk found, not yet resolved — resume capability

`pkg/hub/handlers_agent_lifecycle.go` (commit `c130918e`, 24.07) added: the hub looks up resume
support from the **installed harness-config's own capability matrix by name** before falling back
to the embedded-harness default. Base `claude/config.yaml` **has no `capabilities.resume` key at
all** (verified above) — only `claude-tmux` declares it. If the overlay collapses both configs
into one directory named `claude`, the merged capabilities block must still carry
`resume: {support: yes}` **only for the tmux-runtime overlay path**, not unconditionally for
container-mode `claude` (unclear whether container-mode resume is expected to work the same way —
not checked yet). Needs either: the overlay merge to inject the capability, or a hub-side runtime
check added to `harnessSupportsResume`. **Not resolved in this pass — flagging for design
decision**, not guessing at the fix.

## Open questions before implementation (need area-scion / user input)

1. Overlay trigger: automatic on `RuntimeName == "tmux"` (invisible to the operator) vs. an
   explicit flag in the harness-config that opts a harness into tmux support? Automatic is what
   the task body asks for, but it also means `claude`'s YAML now encodes two auth surfaces in one
   file — worth confirming that's the intended ergonomics before writing the merge code.
2. `claude-tmux-skills` — same overlay + a skills-list diff, or does it stay a separate directory
   layered on top of the (now-merged) `claude` overlay? Not diffed yet.
2. Migration/backward-compat: fleet has live agents with `harness: claude-tmux` pinned in their
   `agent-info.json`/template refs (M1 templates, per `scion-fleet-upgrade-plan.md`). Deprecating
   the directory outright breaks resume/relaunch for those until they're migrated. Proposed:
   keep `claude-tmux` and `claude-tmux-skills` installed as thin *pointer* configs during a
   transition window (documented deprecated, `provisioner`/`auth` blocks empty, `harness: claude`
   redirect) rather than deleting them — needs confirmation this satisfies the "no fleet downtime"
   invariant from the task.
3. DoD asks for a live spawn-test on both M1 and M5 (manual-auth from HOME, `agents.md` in
   context, resume session-pinning) — that's the actual acceptance gate; this note is scoping
   only, not that test.

## Next step

Park here pending a decision on Q1 (auto vs. opt-in overlay) — that decision shapes the schema
change, which shapes everything downstream. Task left `In Progress`, not started on code.
