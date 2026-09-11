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

## Design reversal (user directive, 2026-09-08) and what step 1 actually found

The overlay above is being reverted. User's thesis: runtime differences must be solved inside the
tmux runtime implementation, and every standard harness must keep working unchanged. Agreed plan:
(1) capability surface on `Runtime`; (2) runtime-aware provisioner execution instead of swapping
`provisioner.type` to `builtin`; (3) auth gate by capability instead of pruning `auth.types`;
(4) instructions via the normal path so `CLAUDE_CODE_ADDITIONAL_DIRECTORIES_CLAUDE_MD` and
`--add-dir` disappear; (5) revert `e7a3fce3`/`14732ad1`/`8376541a`; (6) pointer-configs on M1/M5.

Scoping step 1 surfaced something that reframes steps 2–4.

### The overlay's justification rests on a premise that is false in this code

`harnesses/claude/config.yaml:147-157` states the tmux agent "runs as a host process inheriting the
operator's real HOME (never a container)", and derives from that: builtin provisioner (nothing to
provision), auth restricted to manual/api-key (no "provisioned container credential path"), and
`--add-dir` + `CLAUDE_CODE_ADDITIONAL_DIRECTORIES_CLAUDE_MD` as "the only route" for agent
instructions.

The tmux agent does **not** run in the operator's HOME:

- `buildEnvFlags` sets `HOME=config.HomeDir`, the agent's own home (`pkg/runtime/tmux.go:326`).
  `home_mode=system` — the mode that would have preserved the operator's HOME — was removed in
  `1274ca1b`. (Correction 2026-09-11: "never set in any real settings.yaml on this fleet" was
  wrong — M1's global settings.yaml carried it in the unused `tmux-hub` profile; the new binary
  rejects it, and the profile entry was dropped during the step-6 rollout.)
- Verified live on 2026-09-09: a tmux agent's Claude wrote its session transcript to
  `<agentHome>/.claude/projects/…jsonl`, not to the operator's `~/.claude/projects`.
- The agent home **is** a provisioned credential path: `serializeSecrets(config.HomeDir, …)`
  (`tmux.go:126`) stages file secrets to targets expanded against the agent home, and
  `sciontool init --tmuxruntime` writes them — the same pipeline docker/podman/k8s use. Env
  secrets are injected as tmux `-e` flags (`tmux.go:343-348`).

So tmux has its own home, its own credential staging, and its own env injection. Each of the three
justifications above is unsupported by the code as it now stands.

### Consequence: steps 2–4 are mostly deletions, not capability checks

- **Provisioner (step 2).** Execution is already runtime-neutral: `sciontool` runs the provisioner
  as a plain subprocess (`cmd/sciontool/commands/harness.go:169`), and under tmux sciontool already
  runs on the host with `HOME=agentHome`. The one container-shaped thing is the literal path in
  `provisioner.command` (`["python3", "/home/scion/.scion/harness/provision.py"]`). Every other
  manifest path is already `$HOME`-relative and expanded by `resolveManifestHomePaths`
  (`harness.go:237-261`) — which does not cover `Provisioner.Command`. Making the command
  `$HOME`-relative and resolving it there very likely removes the need for the `builtin` swap
  outright. Confirmed empirically that live tmux agents ship `provisioner: {type: builtin}` with
  no command at all, so `provision.py` never runs on this fleet today.
- **Auth (step 3).** No Go code branches on auth type — repo-wide grep for `"manual"` outside tests
  returns nothing; the restriction is 100% the YAML's own `types:` map. With file-secret staging
  working under tmux, `auth-file` has no mechanical reason to be disabled.
- **Instructions (step 4).** `instructions_file: .claude/CLAUDE.md` is declarative and the agent
  home is a real host directory, so instructions can be written to the well-known path exactly as
  in container mode. `CLAUDE_CODE_ADDITIONAL_DIRECTORIES_CLAUDE_MD` and `--add-dir` appear only in
  this config's own overlay block — no Go or Python code references either.

### What that leaves for a capability surface

Genuine, non-dissolving differences: tmux has no image (`ImageExists`/`ImageID`/`PullImage`/
`RemoveImage` are meaningless) and transfers no workspace (`Sync`, and the client-side workspace
collection at `cmd/common.go:899` — measured at ~34 s against the vault, for files the tmux runtime
will never move).

The workspace one cannot be gated on a runtime capability as things stand: `startAgentViaHub` has
no `Runtime` in scope, and the runtime is resolved **broker-side** — confirmed 2026-09-09,
`scion start -p m5t` put the agent in `RUNTIME=container` twice, because the client profile does
not reach the broker. Gating it needs the hub to tell the client whether workspace files are
wanted; that is a protocol change, not a capability interface. Left alone.

## Step 1 implemented — `Runtime` capability surface

Shape mirrors `Diagnosable` (`pkg/runtime/doctor.go:32`): an optional interface, so adding a
capability never forces an edit to runtimes it does not concern.

- `pkg/runtime/capabilities.go` — `Capabilities{Images, LocalImageStore}`, `CapabilityReporter`,
  `ContainerCapabilities()` (the shape every call site already assumed), and `CapabilitiesOf(rt)`.
  A nil runtime reports the zero value, not the default: callers guard nil precisely so they can
  skip work that would dereference it, and container defaults would turn a skipped step into a
  panic. Found while wiring — the original code guarded `m.Runtime != nil` for exactly that reason.
- Reporters on the three runtimes that differ from the Docker-shaped default: `TmuxRuntime`
  (no images at all), `KubernetesRuntime` and `CloudRunRuntime` (images, but pulled on the node,
  so a local existence check answers nothing). Docker/Podman/Apple Container stay untouched and
  keep the default.

Only fields with a real caller are included. A capability nobody reads is undetectable when wrong,
which is how the reverted overlay shipped half-wired. `WorkspaceSync` was drafted and dropped for
that reason: its only consumer would be `cmd/sync.go:261`, where the tmux runtime returns `nil`
from `Sync` so `scion sync` reports success while doing nothing — a real bug, but fixing it is a
user-visible UX change unrelated to this task. Flagged, not changed.

### Consumers wired (all in `pkg/agent/run.go`)

1. The local-image check no longer keys on a hardcoded name list
   (`"docker" || "podman" || "container" || "apple-container"`) but on `LocalImageStore`. That list
   carried a dead entry: `AppleContainerRuntime.Name()` is `"container"`, never `"apple-container"`
   — the kind of silent drift a self-describing runtime cannot produce.
2. `no container image resolved` is no longer fatal for a runtime without images. Host-execution
   harness-configs currently name an image nobody uses, purely to satisfy this check.
3. The pre-launch `ImageExists`/`PullImage` pair now runs only when the runtime has images. This
   check was unconditional, and it is *why* `TmuxRuntime.ImageExists` returns `true, nil`: a
   runtime with no images had no way to say the question did not apply, so it lied to stop the
   pull from firing. The stub stays for callers holding a bare `Runtime`; callers that consult
   capabilities no longer need it.

### Verification

`TestCapabilities_LocalImageStoreMatchesReplacedNameList` pins the refactor to the same answer the
name list gave for every runtime it covered, so a behaviour change has to be deliberate. Removing
the tmux reporter makes it and `TestCapabilitiesOf_Reporters/tmux` fail — checked by stash, not
assumed. The `pkg/agent` suite has 4 pre-existing failures (`TestProvisionAgent*`,
`TestProvisionGeminiAgentSettings`), identical on the clean base — verified by stashing all six
changed files, including moving the two untracked ones aside, after a first attempt silently
stashed nothing and produced a worthless comparison.

End to end on the local hub: a `claude-tmux` agent created, started (`RUNTIME=tmux`), booted in
tmux window `global--cap-e2e#` under the branch `sciontool`, and torn down cleanly — with the image
checks now skipped rather than answered by a stub.

### Correction to this document

Item 5 above (`pkg/agent/provision.go`'s `harness.Resolve` "intentionally left without
`RuntimeName`") is stale: the code at `provision.go:838-853` now passes it from context. The
inline comment claiming otherwise went with the change and the doc did not.

## Step 5 done — the overlay mechanism is gone from the branch

`runtime_overlays` and the home-overlay copy are removed. They were never pushed anywhere, so the
removal is a history rewrite rather than a revert commit: rebasing this branch onto
`origin/tmux-unsafe` dropped the three commits that carried them, and every later commit was
replayed on top.

Everything the sections above describe under **Implemented** — `e7a3fce3`, `14732ad1`, `8376541a` —
therefore no longer exists on this branch. Those hashes resolve only in the pre-sync backup tag.
The sections stay because they record why the mechanism was built and what building it revealed;
they do not describe current code.

What the removal restores:

- `HarnessConfigEntry` loses the `runtime_overlays` field, and `pkg/harness/resolve.go` loses the
  merge logic that applied it.
- `pkg/agent/provision.go` loses the runtime-aware home-overlay directory copy and `pkg/api`
  the types that carried it.
- `harnesses/claude/config.yaml` loses the `runtime_overlays.tmux` block, along with the
  `home-overlays/tmux/**` skeleton it pointed at. The config is back to one shape for every
  runtime, plus the `$HOME`-relative provisioner command from step 2.

The measurable effect: the four `TestClaude*` failures in `pkg/harness` are gone. They were red
because `harnesses/claude/config.yaml` no longer validated against the settings schema — the
in-tree symptom of the same defect the smoke test hit on the fleet. `pkg/harness` is green.

### Verification

Full `go test ./pkg/... ./cmd/...` on this branch and on `origin/tmux-unsafe`, failure sets
compared: identical, package for package (`pkg/agent`, `pkg/config`, `pkg/hub`, `pkg/hubsync`) —
all pre-existing on the shared branch, none introduced here. At test-name granularity for the
three fast packages the two runs differ only in reported durations. `pkg/harness`, red before the
rebase, is green on both.

## Synced with the shared branch (2026-09-10)

This branch had drifted onto an old base: its merge-base with `upstream/main` was `25714622`
(late August), while `origin/tmux-unsafe` had been rebased forward and carried five commits of
parallel work this branch lacked, including `runtimes.<name>.env` plumbing into tmux sessions.

The nine commits unique to this line were replayed onto `origin/tmux-unsafe`. Two conflicts, both
real overlaps rather than noise:

1. `pkg/runtime/tmux.go` / `tmux_test.go` — the parallel `runtimeEnvEntries` helper landed in the
   same region as the `home_mode=system` removal. Resolved by keeping both: the helper, the
   agent-mode env contract test, and the runtime-env ordering test. The system-mode tests went
   with the mode.
2. `pkg/runtime/cloudrun_runtime.go` — the shared branch carries a newer Cloud Run Instances
   implementation. Only the capability reporter was carried over onto it.

`origin/tmux-unsafe` itself is 35 commits behind `upstream/main`. Pulling it forward rewrites a
branch someone else is working on, so that is a shared decision, not a local one. One of those 35
is directly relevant here: `84c58a44 fix(harness/claude): remove invalid settings that block agent
startup (#1513)`.

## Stand campaign (2026-09-10 evening) — stock `claude` under tmux, full boot matrix

Local hub on build `009c0caf`+`d9220199`, stock `claude` harness-config, tmux runtime. Every
claim below was watched live on agents t1–t5; probes were deleted afterwards.

**What now demonstrably works, stock, under tmux:** create → provision (`method=auth-file`) →
harness boots to a working REPL with **no trust dialog and no bypass dialog**; instructions land
on the native path (`~/.claude/CLAUDE.md`) and the agent confirms them in context; `HOME` is the
agent home and cwd the workspace at the process level; suspend → resume relaunches with
`--resume <exact-session-uuid>` (captured by the SessionStart hook); duplicate create/start is
refused by the hub (409) with no window duplication.

**Root cause found and fixed (`d9220199`):** in shared-workspace mode the manifest carries an
empty `agent_workspace`, and the script-side fallback is the container literal `/workspace` —
so Claude Code's project trust was keyed to a directory the harness never runs in, and every
boot re-asked the trust wizard. The pre-start hook runs in the agent's environment with cwd =
the workspace in both worlds (container WORKDIR, `tmux new-window -c`), so sciontool now fills
the empty field from cwd (byte-identical in containers) and `ProvisionContext.workspace` gains
an env fallback. Suppressing the trust wizard also suppresses the bypass-mode dialog — they are
one first-run flow.

**The `.claude.json` skeleton mystery, resolved as not-a-bug:** a 100ms-poll of the file during
boot shows the skeleton arrive (1007B, `bypassPermissionsModeAccepted: true`), the provisioner
merge preserve it, and then Claude Code itself rewrite its state without the key — it is a
legacy key (2.1.197-era) the current version consumes and migrates. The operator's own
`.claude.json` and a long-running fleet agent's both lack it too. Boot is promptless regardless.

**The operator bridge (`pre_start`) was the third layer of the old model, and it must shrink:**
the fleet script symlinks the operator's `~/.claude.json` over the agent's, which used to be the
auth mechanism under `claude-tmux`+builtin, but under the real provisioner it only destroys the
skeleton seed (provision.py then reads operator content through the symlink and atomically
replaces it — the operator's file survives only because the write is tmp+rename). The shipped
example now documents the boundary: link `~/.claude/.credentials.json` (credential bridge — what
pre_start is for), never `~/.claude.json`. The fleet script keeps the old symlink until the
step-6 rollout switches M5/M1 off `claude-tmux`.

**Flagged, not fixed:**
- `no_auth.behavior: drop-to-shell` only triggers when auth-candidates.json is entirely absent;
  an empty-candidates file fails provisioning instead (fail-closed, arguably correct, but the
  config reads as if drop-to-shell would kick in).
- The hub's storage copy of `claude` (stand: `templates/hubs/.../global/claude`) is stale — old
  container-literal provisioner command. Local `~/.scion/harness-configs` won today; a broker
  that falls back to hub storage regresses. Step-6 rollout must refresh hub storage too.
- `claude.provision_test.ModelResolutionTest` is red (3F+8E) on the shared branch independent of
  this work — verified identical on the parent commit.
- The upstream fix `6591cf14` (#1447, as_needed secret keys → broker env-gather) is among the 35
  commits `origin/tmux-unsafe` is behind; without it the hub's `CLAUDE_CODE_OAUTH_TOKEN`
  (as_needed) never reaches a tmux agent, which is why auth rides the file bridge today.

## 2026-09-11 — Step 6 executed: the fleet left `claude-tmux`

The rollout ran as a two-agent campaign (tmux-unsafe-m5 from M5, epic-scion-tmux on M1/vm3) and
finished the deprecation in one day:

- **Pointers:** all 40 vault templates' `lsa.harness_config` → `claude`; both
  `default_harness_config` pointers in the project settings.yaml; hub-storage copy of `claude`
  refreshed (the stale-copy flag above is resolved); the hub project pre-start hook no longer
  symlinks `~/.claude.json` (the fourth and last carrier of the legacy bridge).
- **Binaries:** branch rebased onto `upstream/main` (`ea506bbe`, 52 commits) with the full test
  gate identical to upstream, pushed as `2e4fb62f`. M5 broker, vm3 hub (native build,
  `/opt/scion-hub/bin`, systemd restart, both brokers reconnected in ~1s), M1 broker
  (darwin cross-build on vm3, `~/go/bin`) all run it.
- **Fleet:** M5 — 7 areas + 3 running epics + orchestrator respawned, 2 parked epics re-recorded;
  M1 — 16/16 agents migrated (env preserved; `NUTRI_PERSON_DIR` via `--config` inline), then the
  executor itself respawned last. **0/33 first-run dialogs** — the skeleton + real provisioner
  boot promptless everywhere.
- **Found on the way:** vault agents resolve their runtime from the *project*
  `/Volumes/mch/.scion/settings.yaml` (profile `tmux-obsi`), not the global one — a prior
  `DISABLE_AUTOUPDATER` env fix sat inactive in the global file; and the broker caches runtime
  config at start, so `runtimes.<name>.env` edits need a broker restart to reach new sessions.
- **Upstream candidate:** under an agent-scoped token, `scion start` is denied
  (`project:template:write`) while `scion stop --rm` is allowed — an asymmetry in the
  delegation scopes worth a look.

Remaining `claude-tmux` records: none in the running fleet. The `claude-tmux`/`claude-tmux-skills`
config directories stay on disk as rollback fallback until the operator deletes them.
