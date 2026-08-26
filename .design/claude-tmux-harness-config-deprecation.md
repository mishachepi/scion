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

## Decisions received (area-scion, 2026-08-26)

- **Q1:** the sketch above *is* the design — trigger is `runtime_overlays.<name>` presence +
  `RuntimeName` match, declarative opt-in at the YAML level, automatic at the operational level.
  No per-harness-name branching in Go; generic merge mechanics only.
- **Resume risk:** capability is injected *only* through the tmux overlay, never
  unconditionally — and the merge must be **one shared function** called by both the agent launch
  path and the hub's `harnessSupportsResume` (two independent merge paths is exactly the drift
  class `c130918e` fixed).
- **Q3 (backward-compat):** thin pointer-configs (`deprecated`, `harness: claude` redirect)
  satisfy the no-downtime invariant for the transition window. Final fleet-template migration
  (`.scion/templates/**`) is **orchestrator's lane**, not this task's — area-scion raises it
  separately once the code is ready.
- **Scope boundary:** code (fork) + pointer-configs + the DoD spawn-test are this task's scope.
- Stale-comment fix: in scope, do it.

## Implemented (this session, commit `e7a3fce3`)

1. `HarnessConfigEntry.RuntimeOverlays map[string]*HarnessConfigEntry` (`pkg/config/settings_v1.go`).
2. `harness.ResolveForRuntime(entry, runtimeName)` — the one shared merge path, built on an
   extended `mergeHarnessConfigEntries` (was settings-overlay-only; now covers the full entry
   field set: Provisioner/Command/Capabilities/Auth/NoAuthConfig/MCP replaced wholesale when the
   overlay sets them, maps merged key-wise, scalars/slices replaced when non-empty).
3. Wired into `harness.Resolve()` (`pkg/harness/resolve.go`), called from `pkg/agent/run.go`
   (`m.Runtime.Name()` was already in scope there) — agent launch path.
4. Wired into `pkg/hub/handlers_agent_lifecycle.go`'s `harnessSupportsResume` /
   `installedHarnessCapabilities`, now taking `agent.Runtime` and resolving through the same
   `ResolveForRuntime` — hub path.
5. `pkg/agent/provision.go`'s `harness.Resolve()` call (skills-dir copy) intentionally left
   without `RuntimeName` — no runtime is known at that provisioning stage yet; documented inline.
6. Not build-verified locally (Mac local-build ban — I hit this mid-session, killed a `go build`
   after it started downloading modules, no disk damage, but stopping the practice). `gofmt -l`
   clean on all five touched files. **Needs the remote build/test channel before this is
   considered gated**, same gap the epic's cadence cycles 4/5 already flagged for area-scion's
   lane.

## New finding — home-skeleton content is a second, separate gap (not yet solved)

Diffing the **full directories**, not just `config.yaml` (`diff -rq claude/ claude-tmux/`)
surfaces content the config.yaml overlay mechanism above does **not** touch, because
`ProvisionAgent`'s file-copy steps (`pkg/agent/provision.go`, "Copy harness-config base home →
agentHome") read `hcDir.Path/home/**` directly off disk — independent of the in-memory
`HarnessConfigEntry` merge:

- `claude-tmux/home/.claude/skills/README.md` — a default skill, **absent from `claude/home/`**.
- `claude-tmux/home/.claude/settings.json` vs `claude/home/.claude/settings.json` — **not a
  trivial diff**: claude-tmux's carries `outputStyle`, `enabledPlugins` (obsidian/qmd/playwright/
  dotfiles), a tool `deny` list (13 entries — EnterPlanMode, SendMessage, PushNotification, etc.),
  `disableBundledSkills`/`disableWorkflows`/`disableRemoteControl`/`disableClaudeAiConnectors`/
  `disableArtifacts`, `skipDangerousModePermissionPrompt`, and a `statusLine` command — **none of
  which exist in `claude/home/.claude/settings.json`**.
- `claude/home/.bashrc` and `claude/home/.claude.json` exist only on the container side (make
  sense — tmux runtime inherits the operator's real `$HOME`, doesn't need a container home
  skeleton for those).
- `claude/` also carries `provision.py` + `capture_auth.py` (container-script payloads) that
  `claude-tmux` doesn't ship — harmless to leave in place (the merged `Provisioner.Type: builtin`
  means `ContainerScriptHarness` never invokes them, confirmed by reading
  `container_script_harness.go`'s builtin-type short-circuit), but worth a comment when the
  directories consolidate so the next reader isn't confused why an unused script sits there.

**Why this matters for "pointer-configs":** a thin `claude-tmux/config.yaml` that just says
`harness: claude` (redirecting resolution to the `claude` directory) would silently drop the
settings.json payload and the skills README for every tmux agent — a real behavioral regression,
not a paper cut (the deny-list and disabled-features block in particular look like deliberate
safety/scope choices for tmux-runtime agents specifically, not incidental drift).

**Not deciding this myself** — two directions are visible and I don't know which the operator
intends:
(a) fold `claude-tmux/home/**` content into `claude/home/**` outright (on the theory that this is
operator-preference config that should apply to every `claude` agent regardless of runtime, not a
tmux-specific safety boundary), or
(b) make `ProvisionAgent`'s home-copy step runtime-aware too — copy a `home/` overlay directory
analogous to `runtime_overlays.tmux` in config.yaml (e.g. `runtime_overlays/tmux/home/**`) on top
of the base `home/`, mirroring the config.yaml mechanism instead of collapsing content into one
shared skeleton.
(b) keeps parity with the "declarative, additive, opt-in" shape of the config.yaml change; (a) is
simpler but changes behavior for every container-mode claude agent, which nobody asked for and
which I have no evidence is intended.

## Decision received (area-scion, 2026-08-26, second round) + a correction to my own premise

- My "deny-list only exists in claude-tmux" claim was wrong — checked only M5's installed copy.
  area-scion checked M1 directly: the mesh-hardening block (deny-list, disable-flags, hooks,
  `includeCoAuthoredBy`/`gitAttribution`) **is** in M1's installed `claude/home/` already. M5's
  install had drifted (fleet-upgrade-plan gap #10, tracked separately, not this task).
- Split decided: runtime-agnostic mesh-hardening → base `claude/home/` (canon = M1/newer). Tmux
  host-specifics (`statusLine`, `enabledPlugins`, `outputStyle`, `tui: fullscreen`,
  `skipDangerousModePermissionPrompt`, harness-level `skills/`) → `home-overlays/tmux/`, my
  option (b), copied by `ProvisionAgent` on top of base when runtime matches — implemented, see
  below. `settings.json` needs a real shallow JSON merge, not file-overwrite — implemented.

## Re-grounded on the repo, not the installed configs (important correction)

Everything above this point in the doc was diffed against **installed** `~/.scion/harness-configs/`
on M5 — I hadn't checked whether `harnesses/claude/` in the **repo** (the actual
upstream-contributable source, and what `home-overlays/tmux/` needs to be added to) matches. It
doesn't — the repo is *further ahead* than either machine's install:

- Repo `harnesses/claude/config.yaml` **already has** `resume_id_flag`, `capabilities.resume:
  yes`, `auth.types.manual: {}`, and `command.base` using `--permission-mode bypassPermissions`
  (not `--dangerously-skip-permissions`) — all things I thought were claude-tmux-only. Someone
  already moved these into the fork's base `claude` ahead of this task.
- Repo `harnesses/claude/home/.claude/settings.json` **already has** the full mesh-hardening
  block (deny-list, disable-flags, hooks, `includeCoAuthoredBy`/`gitAttribution`) — matches what
  area-scion confirmed on M1. Base is already correct; nothing to move here.
- **True residual** (repo `claude` vs installed `claude-tmux`, clean diff, hooks-formatting noise
  excluded): `outputStyle: "Assistant General"`, `enabledPlugins` (`obsidian@obsidian-skills`,
  `qmd@qmd`, `playwright@claude-plugins-official`, `mch@dotfiles`),
  `skipDangerousModePermissionPrompt: true`, `statusLine: {type: command, command:
  "~/.claude/statusline.sh"}`, `tui: "fullscreen"`. `statusline.sh` itself is **not** shipped by
  any harness-config — `~/.claude/statusline.sh` on the operator's real machine is a symlink into
  their own dotfiles; the setting is just a path reference, nothing to copy.
- `claude-tmux/home/.claude/skills/README.md` is documentation only (explains an empty
  `skills/` directory convention) — no actual skill content, trivial to carry over as-is.
- Remaining `provisioner`/`auth.default_type`/`env.CLAUDE_CODE_ADDITIONAL_DIRECTORIES_CLAUDE_MD`
  diffs from the original table still stand and are genuinely runtime-mechanical.

**One more split I'm flagging rather than deciding:** of that residual settings.json list,
`statusLine`/`tui: fullscreen`/`skipDangerousModePermissionPrompt` are runtime-mechanical (tmux
agents own a real terminal, unlike containers — generic, upstream-clean). `outputStyle` and
`enabledPlugins` name *this operator's* personal plugin ecosystem (`mch@dotfiles`,
`obsidian@obsidian-skills`) — shipping those in `harnesses/claude/home-overlays/tmux/` would put
personal config into the upstream-submittable fork source. My proposal: ship only the three
mechanical keys in the repo overlay; `outputStyle`/`enabledPlugins` stay something the operator
sets on their own *installed* config post-install (same tier as any other personal Claude Code
preference), not fork-shipped. Proceeding on this reading unless told otherwise — it's a narrow,
reversible call (one overlay JSON file) and blocking a `не срочно` task on it a third time doesn't
pay for itself.

## Implemented (continued)

7. `api.ContextWithRuntimeName` / `RuntimeNameFromContext` (`pkg/api/types.go`) — threads the
   resolved runtime name into `ProvisionAgent` via context (not a new positional parameter;
   `GetAgent`/`ProvisionAgent` have 50+ existing positional test call sites, a context key needs
   zero test changes since unset = "" = no-op).
8. `applyRuntimeHomeOverlay` + `mergeJSONFileShallow` (`pkg/agent/provision.go`) — copies
   `home-overlays/<runtime>/` onto the agent home between the base-home copy and the
   template-home copy; `.json` files present in both get a shallow top-level-key merge instead of
   an overwrite.
9. Corrected an inaccurate comment from the previous commit ("no runtime known at
   ProvisionAgent's point") — `Start()`/`Provision()` both have `m.Runtime` in scope before
   calling `GetAgent`; I'd only checked `ProvisionAgent`'s own local scope, not its callers.

## Implemented (continued, commit `8376541a`)

10. `runtime_overlays.tmux` written into the repo's `harnesses/claude/config.yaml`: provisioner→
    builtin, `env.CLAUDE_CODE_ADDITIONAL_DIRECTORIES_CLAUDE_MD=1` (the one genuinely-missing env
    key — command/base args needed no overlay, base config.yaml already converged), a matching
    `capabilities` block, and `auth` restricted to manual/api-key. YAML validated (isolated venv,
    pyyaml — no repo/system dependency added; can't run `go build` locally to validate via the
    real parser).
11. `harnesses/claude/home-overlays/tmux/home/.claude/settings.json` — the three mechanical
    residual keys (`statusLine`, `tui`, `skipDangerousModePermissionPrompt`). Deliberately
    excludes `outputStyle`/`enabledPlugins` (operator-personal, see decision below).
12. `harnesses/claude/home-overlays/tmux/home/.claude/skills/README.md` — carried over from the
    installed claude-tmux copy, reworded off the "claude-tmux" name onto "claude (tmux runtime)".
13. Needed `git add -f` for the new `home-overlays/**/.claude/` paths — a repo-wide `.claude/`
    gitignore rule matches them; the pre-existing base `harnesses/claude/home/.claude/` predates
    that rule and stayed tracked regardless.

## What's left (blocked on infrastructure I don't own, not a design question)

- **Pointer-configs** for the *installed* `claude-tmux`/`claude-tmux-skills` on M1/M5 — these
  never existed in the repo (confirmed: no `harnesses/claude-tmux/` at any point), so there is
  nothing to deprecate in-repo. This is an operational step against each machine's
  `~/.scion/harness-configs/`, not a commit.
- **Remote build** — none of the code above is compiled or tested; local `go build` is banned on
  this Mac (disk incident) and I have no vm3/CI access of my own from the M5 agent environment.
  Needs the remote build channel (`scion-vm3-build.sh` or fork CI) before any of this can be
  trusted, same gate every cadence cycle in this epic has needed.
- **DoD spawn-test on M1+M5** depends on both of the above: the installed `claude/` config needs
  the new `config.yaml`/`home-overlays/` content, and the binary running needs to actually be
  built from this branch — testing against the currently-installed stale binaries would validate
  nothing.

Reported to area-scion; task stays `In Progress` pending the build channel and a
go/no-go on the outputStyle/enabledPlugins exclusion.
