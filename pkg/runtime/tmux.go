// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package runtime

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/GoogleCloudPlatform/scion/pkg/api"
	"github.com/GoogleCloudPlatform/scion/pkg/projectcompat"
	"github.com/GoogleCloudPlatform/scion/pkg/util"
)

// DefaultTmuxSession hosts all scion-managed windows when TmuxRuntime.Session
// is unset.
const DefaultTmuxSession = "scion"

// HomeMode values for TmuxRuntime.HomeMode. Empty defaults to HomeModeAgent.
const (
	HomeModeAgent  = "agent"
	HomeModeSystem = "system"
)

// User-option key namespaces. tmux requires user-option names to start with "@".
const (
	tmuxLabelPrefix      = "@scion-label-"
	tmuxAnnotationPrefix = "@scion-annotation-"
	tmuxWorkspaceOption  = "@scion-workspace"
	// tmuxPaneOption persists the agent's pane_id on the window so
	// send-keys/paste-buffer target the agent's pane explicitly, even
	// after a user-driven split shifts "the active pane" or a tmux
	// server restart re-issues pane_ids. Refreshed by sciontool's
	// SessionStart hook (see pkg/sciontool/hooks/handlers/tmux_pane.go).
	tmuxPaneOption = "@scion-pane"
	// tmuxHomeOption records the agent's home directory so Exec can
	// impersonate the agent (HOME, cwd, SCION_* env) instead of running
	// as the broker user — the host-execution analogue of container exec.
	tmuxHomeOption = "@scion-home"
)

// ValidateTmuxSessionName rejects names tmux would misparse as target
// addresses. tmux uses ':' as session/window and '.' as window/pane separator,
// so a session named "lsa.vault" makes "lsa.vault:@5" ambiguous.
func ValidateTmuxSessionName(name string) error {
	if name == "" {
		return fmt.Errorf("tmux session name must not be empty")
	}
	if strings.ContainsAny(name, ":.") {
		return fmt.Errorf("tmux session name %q must not contain ':' or '.' (tmux uses them as target separators)", name)
	}
	return nil
}

// TmuxRuntime runs scion agents as host processes inside tmux windows — no
// containers, no images, no workspace sync. Identifiers returned by Run are
// qualified tmux targets ("<session>:<window_id>", e.g. "scion:@5").
type TmuxRuntime struct {
	Command string
	Session string

	// PreStartScript is invoked before each new-window with arguments
	// <agentHome> <operator-$HOME>. Skipped in HomeModeSystem.
	PreStartScript string

	// HomeMode controls HOME and env injection. See V1RuntimeConfig.HomeMode.
	HomeMode string

	// Sciontool is an absolute sciontool-binary path used to wrap each
	// harness in `sciontool init --tmuxruntime --`. Empty disables wrapping.
	// The "auto" sentinel is resolved to an absolute path in factory.GetRuntime.
	Sciontool string
}

func NewTmuxRuntime() *TmuxRuntime {
	return &TmuxRuntime{Command: "tmux", Session: DefaultTmuxSession, HomeMode: HomeModeAgent}
}

func (r *TmuxRuntime) Name() string { return "tmux" }

// ExecUser returns "" — the agent runs as the current host user.
func (r *TmuxRuntime) ExecUser() string { return "" }

func (r *TmuxRuntime) Run(ctx context.Context, config RunConfig) (string, error) {
	if r.HomeMode == "" {
		r.HomeMode = HomeModeAgent
	}

	// Per-agent tmux session override. We intentionally piggy-back on
	// KubernetesConfig.Namespace rather than introducing a TmuxConfig
	// struct: the field is already wired end-to-end (template →
	// ScionConfig → runCfg, see pkg/agent/run.go), already per-agent,
	// and the conceptual mapping "namespace = organizational scope" is
	// the same. The k8s runtime keeps its own meaning of the field; the
	// tmux runtime reads it as session name. r.Session remains the
	// runtime-level default when no per-agent override is set.
	session := resolveTmuxSession(r.Session, config)

	args, err := r.buildNewWindowArgs(config, session)
	if err != nil {
		return "", err
	}

	warnIgnoredFeatures(config)
	// Serialize file + variable secrets into the StagedSecretEnvVar blob.
	// `sciontool init --tmuxruntime` (the wrapper that fronts every harness
	// in tmux runtime) decodes the env var and writes secrets to disk —
	// file secrets to their target paths, variable secrets to
	// ~/.scion/secrets.json. Same pipeline as docker / podman / k8s after
	// upstream PR #523 (stateless-broker secret staging).
	if len(config.ResolvedSecrets) > 0 {
		encoded, err := serializeSecrets(config.HomeDir, config.ResolvedSecrets)
		if err != nil {
			return "", fmt.Errorf("tmux runtime: serialize secrets: %w", err)
		}
		if encoded != "" {
			config.Env = append(config.Env, StagedSecretEnvVar+"="+encoded)
		}
	}
	if err := cloneWorkspaceIfRequested(ctx, config); err != nil {
		return "", err
	}

	if err := r.ensureSession(ctx, session); err != nil {
		return "", fmt.Errorf("tmux runtime: ensure session %q: %w", session, err)
	}

	// Refuse explicit collision rather than silently creating a duplicate
	// window with the same scion.name. List() walks all sessions via
	// `list-windows -a` + scion.name label, so a per-agent session
	// override does not let a slug exist twice across sessions.
	if scionName := scionNameFromRunConfig(config); scionName != "" {
		existing, listErr := r.List(ctx, map[string]string{"scion.name": scionName})
		if listErr == nil && len(existing) > 0 {
			return "", fmt.Errorf(
				"tmux runtime: agent %q already exists at %s — delete it first (scion delete %s) or attach (scion attach %s)",
				scionName, existing[0].ContainerID, scionName, scionName)
		}
	}

	if err := r.runPreStartScript(ctx, config.HomeDir); err != nil {
		return "", err
	}

	// Reap a stale window with the same name in the target session. Tmux
	// user-options (where scion.name lives) don't survive a tmux server
	// restart, so the List() check above misses windows whose harness died
	// with the crash; the leftover zsh shell still occupies the slot. Without
	// this, new-window would silently create a duplicate window with the
	// same display name, leaving two `<project>--<agent>` entries in the
	// session list. Killing the stale one first keeps the post-resume
	// window in the original slot with the original name.
	//
	// Re-ensure the session after reaping: tmux auto-destroys a session
	// whose last window is killed, so a one-window session (typical for
	// per-agent obsi-* sessions resolved via Kubernetes.Namespace) vanishes
	// here and the subsequent new-window would fail with "can't find
	// session".
	if id, ok := r.findWindowByName(ctx, session, config.Name); ok {
		if out, killErr := exec.CommandContext(ctx, r.Command,
			"kill-window", "-t", id).CombinedOutput(); killErr != nil {
			return "", fmt.Errorf("tmux runtime: reap stale window %q: %w (output: %s)",
				config.Name, killErr, strings.TrimSpace(string(out)))
		}
		if err := r.ensureSession(ctx, session); err != nil {
			return "", fmt.Errorf("tmux runtime: re-ensure session %q after reap: %w", session, err)
		}
	}

	// Direct exec rather than runSimpleCommand so resolved secret values in
	// envFlags do not appear in the runtime debug log.
	out, err := exec.CommandContext(ctx, r.Command, args...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("tmux new-window failed: %w (output: %s)", err, strings.TrimSpace(string(out)))
	}
	windowID := strings.TrimSpace(string(out))
	if windowID == "" {
		return "", fmt.Errorf("tmux new-window returned empty window_id")
	}

	target := formatWindowTarget(session, windowID)
	if err := r.lockWindowName(ctx, target, config.Name); err != nil {
		return target, err
	}
	if err := r.applyMetadata(ctx, target, config); err != nil {
		return target, fmt.Errorf("tmux runtime: agent started at %s but metadata failed: %w", target, err)
	}
	return target, nil
}

// resolveTmuxSession picks the tmux session for an agent. Per-agent
// override piggy-backs on KubernetesConfig.Namespace (see TmuxRuntime.Run
// for the rationale); runtimeDefault is r.Session when no override is set.
func resolveTmuxSession(runtimeDefault string, config RunConfig) string {
	if config.Kubernetes != nil && config.Kubernetes.Namespace != "" {
		return config.Kubernetes.Namespace
	}
	return runtimeDefault
}

func (r *TmuxRuntime) buildNewWindowArgs(config RunConfig, session string) ([]string, error) {
	if config.Harness == nil {
		return nil, fmt.Errorf("tmux runtime: no harness provided")
	}
	if config.HomeDir == "" {
		return nil, fmt.Errorf("tmux runtime: HomeDir is required")
	}
	if config.Name == "" {
		return nil, fmt.Errorf("tmux runtime: Name is required")
	}
	harnessEnv := config.Harness.GetEnv(config.Name, config.HomeDir, config.UnixUsername)
	harnessArgs := config.Harness.GetCommand(config.Task, config.Resume, config.HarnessSessionID, config.CommandArgs)
	if len(harnessArgs) == 0 {
		return nil, fmt.Errorf("tmux runtime: harness %s returned empty command", config.Harness.Name())
	}
	if r.Sciontool != "" {
		harnessArgs = append([]string{r.Sciontool, "init", "--tmuxruntime", "--"}, harnessArgs...)
	}
	workspace := config.Workspace
	if workspace == "" {
		workspace = config.HomeDir
	}

	// Trailing colon disambiguates the target as a *session* — without it
	// tmux uses its target-window resolver (new-window takes -t target-window),
	// which prefix-matches across all sessions and may pick a window that
	// shares the session name. When that happens tmux tries to insert at
	// that window's index and fails with "index N in use". Real incident:
	// session "scion" + a window named "scion" elsewhere → "index 1 in use".
	args := []string{"new-window", "-d", "-t", session + ":", "-n", config.Name, "-c", workspace}
	args = append(args, r.buildEnvFlags(config)...)
	// Harnesses emit container-side paths (e.g. /home/scion/.gemini/...);
	// rewrite to host-side agentHome so they resolve under tmux.
	if r.HomeMode != HomeModeSystem {
		containerHome := util.GetHomeDir(config.UnixUsername)
		for k, v := range harnessEnv {
			if k == "" || v == "" {
				continue
			}
			if containerHome != "" && strings.HasPrefix(v, containerHome+"/") {
				v = config.HomeDir + v[len(containerHome):]
			}
			args = append(args, "-e", fmt.Sprintf("%s=%s", k, v))
		}
	}
	// -P -F prints the new window_id on stdout.
	args = append(args, "-P", "-F", "#{window_id}")
	args = append(args, harnessArgs...)
	return args, nil
}

// findWindowByName returns the tmux window id (e.g. "@42") for the first
// window in `session` whose display name exactly matches `name`. Returns
// ("", false) when no window matches or the lookup fails. Used to detect
// orphan windows from a previous tmux server (whose scion.name labels
// did not survive the restart) so Start can reap them before re-creating
// the agent window with the same name.
func (r *TmuxRuntime) findWindowByName(ctx context.Context, session, name string) (string, bool) {
	if session == "" || name == "" {
		return "", false
	}
	out, err := exec.CommandContext(ctx, r.Command,
		"list-windows", "-t", session+":", "-F", "#{window_id} #{window_name}").CombinedOutput()
	if err != nil {
		return "", false
	}
	for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		fields := strings.SplitN(line, " ", 2)
		if len(fields) != 2 {
			continue
		}
		if fields[1] == name {
			return fields[0], true
		}
	}
	return "", false
}

// lockWindowName disables tmux auto-rename and resets the window name.
// Without this, pane_current_path or the harness process title would
// overwrite the name set by new-window -n.
func (r *TmuxRuntime) lockWindowName(ctx context.Context, target, want string) error {
	for _, opt := range []string{"allow-rename", "automatic-rename"} {
		if out, err := exec.CommandContext(ctx, r.Command,
			"set-window-option", "-t", target, opt, "off").CombinedOutput(); err != nil {
			return fmt.Errorf("tmux runtime: disable %s on %s: %w (output: %s)",
				opt, target, err, strings.TrimSpace(string(out)))
		}
	}
	if out, err := exec.CommandContext(ctx, r.Command,
		"rename-window", "-t", target, want).CombinedOutput(); err != nil {
		return fmt.Errorf("tmux runtime: rename-window %s -> %q: %w (output: %s)",
			target, want, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (r *TmuxRuntime) ensureSession(ctx context.Context, session string) error {
	// Trailing colon: same defensive disambiguation as buildNewWindowArgs —
	// keeps tmux's resolver pinned on sessions and consistent with new-window.
	if err := exec.CommandContext(ctx, r.Command, "has-session", "-t", session+":").Run(); err == nil {
		return nil
	}
	if out, err := exec.CommandContext(ctx, r.Command, "new-session", "-d", "-s", session).CombinedOutput(); err != nil {
		return fmt.Errorf("create session %q: %w (output: %s)", session, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// buildEnvFlags produces tmux -e KEY=VAL flags. SCION_AGENT_HOME is always
// emitted; everything else depends on r.HomeMode. HomeModeSystem returns
// exactly `-e SCION_AGENT_HOME=<agentHome>` and nothing more.
func (r *TmuxRuntime) buildEnvFlags(config RunConfig) []string {
	if r.HomeMode == HomeModeSystem {
		return []string{"-e", fmt.Sprintf("SCION_AGENT_HOME=%s", config.HomeDir)}
	}

	entries := []string{
		fmt.Sprintf("HOME=%s", config.HomeDir),
		fmt.Sprintf("SCION_AGENT=%s", config.Name),
		fmt.Sprintf("SCION_AGENT_HOME=%s", config.HomeDir),
	}
	if config.Project != "" {
		entries = append(entries,
			fmt.Sprintf("SCION_PROJECT=%s", config.Project),
			fmt.Sprintf("SCION_GROVE=%s", config.Project),
		)
	}
	if config.ProjectID != "" {
		entries = append(entries,
			fmt.Sprintf("SCION_PROJECT_ID=%s", config.ProjectID),
			fmt.Sprintf("SCION_GROVE_ID=%s", config.ProjectID),
		)
	}
	entries = append(entries, config.Env...)
	for _, s := range config.ResolvedSecrets {
		if s.Type != "environment" || s.Target == "" {
			continue
		}
		entries = append(entries, fmt.Sprintf("%s=%s", s.Target, s.Value))
	}

	flags := make([]string, 0, len(entries)*2)
	for _, kv := range entries {
		if kv == "" {
			continue
		}
		flags = append(flags, "-e", kv)
	}
	return flags
}

func (r *TmuxRuntime) applyMetadata(ctx context.Context, target string, config RunConfig) error {
	for k, v := range config.Labels {
		if k == "" || v == "" {
			continue
		}
		if err := r.setUserOption(ctx, target, tmuxLabelPrefix+k, v); err != nil {
			return err
		}
	}
	for k, v := range config.Annotations {
		if k == "" || v == "" {
			continue
		}
		if err := r.setUserOption(ctx, target, tmuxAnnotationPrefix+k, v); err != nil {
			return err
		}
	}
	if config.Workspace != "" {
		if err := r.setUserOption(ctx, target, tmuxWorkspaceOption, config.Workspace); err != nil {
			return err
		}
	}
	if config.HomeDir != "" {
		if err := r.setUserOption(ctx, target, tmuxHomeOption, config.HomeDir); err != nil {
			return err
		}
	}
	r.persistPaneID(ctx, target)
	return nil
}

// persistPaneID reads the window's current pane_id and writes it to the
// @scion-pane user-option. Best-effort — failure is logged-then-ignored
// since the value is a routing optimization, not a correctness invariant.
func (r *TmuxRuntime) persistPaneID(ctx context.Context, target string) {
	out, err := exec.CommandContext(ctx, r.Command,
		"display-message", "-p", "-t", target, "#{pane_id}").Output()
	if err != nil {
		return
	}
	pane := strings.TrimSpace(string(out))
	if pane == "" {
		return
	}
	_ = r.setUserOption(ctx, target, tmuxPaneOption, pane)
}

// resolvePaneTarget returns the agent's pane_id from the window's
// @scion-pane user-option, falling back to the window target when the
// option is missing or malformed — keeps legacy windows working without
// backfill. tmux pane_ids always start with '%', so anything else is
// rejected (covers empty values and noisy fake-tmux test doubles).
func (r *TmuxRuntime) resolvePaneTarget(ctx context.Context, target string) string {
	out, err := exec.CommandContext(ctx, r.Command,
		"show-option", "-w", "-v", "-t", target, tmuxPaneOption).Output()
	if err != nil {
		return target
	}
	pane := strings.TrimSpace(string(out))
	if !strings.HasPrefix(pane, "%") {
		return target
	}
	return pane
}

// needsPaneTarget reports whether a tmux subcommand operates on a pane
// (vs. a window/session). For pane-level subcommands we route through
// resolvePaneTarget so user-driven splits don't misdirect input.
func needsPaneTarget(args []string) bool {
	if len(args) == 0 {
		return false
	}
	switch args[0] {
	case "send-keys", "paste-buffer", "display-message", "capture-pane":
		return true
	}
	return false
}

// setUserOption persists a tmux user-option at window scope. The -w flag is
// required — without it tmux defaults to session scope and every Run call
// would overwrite the previous agent's labels.
func (r *TmuxRuntime) setUserOption(ctx context.Context, target, name, value string) error {
	out, err := exec.CommandContext(ctx, r.Command, "set-option", "-w", "-t", target, name, value).CombinedOutput()
	if err != nil {
		return fmt.Errorf("set %s=%s on %s: %w (output: %s)",
			name, value, target, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Stop sends Ctrl-C to the harness, then kills the window. Idempotent.
func (r *TmuxRuntime) Stop(ctx context.Context, id string) error {
	if id == "" {
		return fmt.Errorf("tmux runtime: Stop requires a non-empty id")
	}
	id = r.resolveTarget(ctx, id)
	if !r.windowExists(ctx, id) {
		return nil
	}
	sendTarget := r.resolvePaneTarget(ctx, id)
	_, _ = exec.CommandContext(ctx, r.Command, "send-keys", "-t", sendTarget, "C-c").CombinedOutput()
	return r.killWindow(ctx, id)
}

// Delete removes the agent's tmux window. Idempotent.
func (r *TmuxRuntime) Delete(ctx context.Context, id string) error {
	if id == "" {
		return fmt.Errorf("tmux runtime: Delete requires a non-empty id")
	}
	id = r.resolveTarget(ctx, id)
	if !r.windowExists(ctx, id) {
		return nil
	}
	return r.killWindow(ctx, id)
}

func (r *TmuxRuntime) killWindow(ctx context.Context, id string) error {
	out, err := exec.CommandContext(ctx, r.Command, "kill-window", "-t", id).CombinedOutput()
	if err == nil {
		return nil
	}
	// Race: another caller may have killed the window between windowExists
	// and kill-window. tmux prints "can't find window: <id>" — treat as no-op.
	outStr := strings.TrimSpace(string(out))
	if strings.Contains(outStr, "can't find window") || !r.windowExists(ctx, id) {
		return nil
	}
	return fmt.Errorf("tmux kill-window %s: %w (output: %s)", id, err, outStr)
}

// windowExists checks `list-windows` for an exact window_id match.
// `tmux display-message -t <id> #{window_id}` is unsafe here: when called from
// inside a tmux pane with a non-existent target, it silently uses the current
// pane and reports success.
func (r *TmuxRuntime) windowExists(ctx context.Context, id string) bool {
	session, windowID, ok := splitWindowTarget(id)
	if !ok {
		return false
	}
	out, err := exec.CommandContext(ctx, r.Command, "list-windows", "-t", session+":", "-F", "#{window_id}").CombinedOutput()
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.TrimSpace(line) == windowID {
			return true
		}
	}
	return false
}

// runPreStartScript invokes PreStartScript with arguments <agentHome>
// <operator-$HOME>. Skipped in HomeModeSystem (operator HOME is already
// inherited; the bridge would be redundant).
func (r *TmuxRuntime) runPreStartScript(ctx context.Context, agentHome string) error {
	if r.PreStartScript == "" {
		return nil
	}
	if r.HomeMode == HomeModeSystem {
		fmt.Fprintf(os.Stderr,
			"Warning: tmux runtime: pre_start_script %q skipped in home_mode=system (operator HOME is preserved natively)\n",
			r.PreStartScript)
		return nil
	}
	operatorHome := os.Getenv("HOME")
	scriptPath := r.PreStartScript
	if strings.HasPrefix(scriptPath, "~/") {
		scriptPath = filepath.Join(operatorHome, scriptPath[2:])
	} else if scriptPath == "~" {
		scriptPath = operatorHome
	}
	if !filepath.IsAbs(scriptPath) {
		scriptPath = filepath.Join(operatorHome, scriptPath)
	}
	info, err := os.Stat(scriptPath)
	if err != nil {
		return fmt.Errorf("tmux runtime: pre_start_script %q: %w", scriptPath, err)
	}
	if info.IsDir() {
		return fmt.Errorf("tmux runtime: pre_start_script %q is a directory", scriptPath)
	}
	if info.Mode()&0o111 == 0 {
		return fmt.Errorf("tmux runtime: pre_start_script %q is not executable (chmod +x)", scriptPath)
	}
	out, err := exec.CommandContext(ctx, scriptPath, agentHome, operatorHome).CombinedOutput()
	if err != nil {
		return fmt.Errorf("tmux runtime: pre_start_script %q failed: %w (output: %s)",
			scriptPath, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func splitWindowTarget(id string) (session, windowID string, ok bool) {
	i := strings.Index(id, ":@")
	if i <= 0 || i+2 >= len(id) {
		return "", "", false
	}
	return id[:i], id[i+1:], true
}

// resolveTarget maps a caller-supplied id (scion slug or qualified target) to
// a qualified tmux target ("<session>:@<n>").
func (r *TmuxRuntime) resolveTarget(ctx context.Context, id string) string {
	if strings.Contains(id, ":@") {
		return id
	}
	if agents, err := r.List(ctx, map[string]string{"scion.name": id}); err == nil && len(agents) > 0 {
		return agents[0].ContainerID
	}
	return id
}

func (r *TmuxRuntime) List(ctx context.Context, labelFilter map[string]string) ([]api.AgentInfo, error) {
	// list-windows -a iterates every window across every session on the
	// tmux server. The runtime's r.Session is where Start() creates new
	// windows, but a single broker may host agents in any session — e.g.
	// when project settings override the session name per profile
	// (see `runtimes.<name>.session` in settings.yaml). Scoping List to
	// r.Session alone would hide those, making Lookup/Attach/PTY return
	// "agent not found" for legitimately running agents.
	//
	// If no tmux server is running, list-windows exits non-zero with
	// "no server running on …". Treat that as "no agents visible" rather
	// than a hard error, matching the prior r.sessionExists() guard.
	out, err := exec.CommandContext(ctx, r.Command,
		"list-windows", "-a",
		"-F", "#{session_name}:#{window_id}").CombinedOutput()
	if err != nil {
		return nil, nil
	}

	var agents []api.AgentInfo
	for _, target := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if target == "" {
			continue
		}

		meta, err := r.readMetadata(ctx, target)
		if err != nil {
			continue
		}
		// Skip non-scion windows (no scion.name label).
		name := meta.labels["scion.name"]
		if name == "" {
			continue
		}

		// Merge labels and annotations for filtering — broadcast paths filter
		// on project-path keys (canonical and legacy) that docker stores
		// as labels but tmux stores as annotations.
		filterable := mergeLabelMaps(meta.labels, meta.annotations)
		if !matchesLabelFilter(filterable, labelFilter) {
			continue
		}
		project := projectcompat.ProjectNameFromLabels(meta.labels)
		projectPath := projectcompat.ProjectPathFromLabels(meta.annotations)
		if projectPath == "" {
			projectPath = meta.workspace
		}

		agents = append(agents, api.AgentInfo{
			ContainerID:     target,
			Name:            name,
			Phase:           "running",
			ContainerStatus: "running",
			Runtime:         r.Name(),
			Labels:          filterable,
			Annotations:     meta.annotations,
			Template:        meta.labels["scion.template"],
			Project:         project,
			ProjectPath:     projectPath,
			HarnessConfig:   meta.labels["scion.harness_config"],
			HarnessAuth:     meta.labels["scion.harness_auth"],
		})
	}
	return agents, nil
}

func mergeLabelMaps(labels, annotations map[string]string) map[string]string {
	out := make(map[string]string, len(labels)+len(annotations))
	for k, v := range annotations {
		out[k] = v
	}
	for k, v := range labels {
		out[k] = v
	}
	return out
}

func (r *TmuxRuntime) sessionExists(ctx context.Context) bool {
	return exec.CommandContext(ctx, r.Command, "has-session", "-t", r.Session+":").Run() == nil
}

type windowMetadata struct {
	labels      map[string]string
	annotations map[string]string
	workspace   string
	home        string
}

func (r *TmuxRuntime) readMetadata(ctx context.Context, target string) (windowMetadata, error) {
	out, err := exec.CommandContext(ctx, r.Command, "show-options", "-w", "-t", target).CombinedOutput()
	if err != nil {
		return windowMetadata{}, fmt.Errorf("tmux show-options -t %s: %w (output: %s)",
			target, err, strings.TrimSpace(string(out)))
	}
	meta := windowMetadata{labels: map[string]string{}, annotations: map[string]string{}}
	for _, line := range strings.Split(string(out), "\n") {
		name, value, ok := parseUserOption(line)
		if !ok {
			continue
		}
		switch {
		case strings.HasPrefix(name, tmuxLabelPrefix):
			meta.labels[name[len(tmuxLabelPrefix):]] = value
		case strings.HasPrefix(name, tmuxAnnotationPrefix):
			meta.annotations[name[len(tmuxAnnotationPrefix):]] = value
		case name == tmuxWorkspaceOption:
			meta.workspace = value
		case name == tmuxHomeOption:
			meta.home = value
		}
	}
	return meta, nil
}

func parseUserOption(line string) (name, value string, ok bool) {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "@") {
		return "", "", false
	}
	spaceIdx := strings.IndexAny(line, " \t")
	if spaceIdx <= 0 {
		return "", "", false
	}
	return line[:spaceIdx], strings.Trim(strings.TrimSpace(line[spaceIdx+1:]), "\""), true
}

func matchesLabelFilter(labels, filter map[string]string) bool {
	for k, want := range filter {
		if labels[k] != want {
			return false
		}
	}
	return true
}

func scionNameFromRunConfig(config RunConfig) string {
	if config.Labels != nil {
		if v := config.Labels["scion.name"]; v != "" {
			return v
		}
	}
	if config.Name != "" {
		return api.Slugify(config.Name)
	}
	return ""
}

func (r *TmuxRuntime) GetLogs(ctx context.Context, id string) (string, error) {
	if id == "" {
		return "", fmt.Errorf("tmux runtime: GetLogs requires a non-empty id")
	}
	id = r.resolveTarget(ctx, id)
	out, err := exec.CommandContext(ctx, r.Command,
		"capture-pane", "-p", "-J", "-S", "-", "-E", "-", "-t", id).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("tmux capture-pane -t %s: %w (output: %s)",
			id, err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func (r *TmuxRuntime) Attach(ctx context.Context, id string) error {
	if id == "" {
		return fmt.Errorf("tmux runtime: Attach requires a non-empty id")
	}
	return runInteractiveCommand(r.Command, "attach", "-t", r.resolveTarget(ctx, id))
}

func (r *TmuxRuntime) ImageExists(_ context.Context, _ string) (bool, error) { return true, nil }
func (r *TmuxRuntime) PullImage(_ context.Context, _ string) error           { return nil }

// ImageID: agents run as host processes — there are no local images.
func (r *TmuxRuntime) ImageID(_ context.Context, _ string) (string, error) { return "", nil }

// RemoveImage: agents run as host processes — there are no local images.
func (r *TmuxRuntime) RemoveImage(_ context.Context, _ string) error { return nil }
func (r *TmuxRuntime) Sync(_ context.Context, _ string, _ SyncDirection) error {
	return nil
}

// Exec dispatches on cmd[0]: a tmux subcommand is forwarded to host tmux with
// -t rewritten to id; any other binary runs host-side with the agent's
// identity (HOME=agent home, cwd=workspace, SCION_* markers) — the
// host-execution analogue of container exec, so hub actions written with
// ~-relative paths (reset-auth token writes, scion exec) behave the same in
// both worlds. Windows without a recorded @scion-home (started by a
// pre-upgrade binary, or after a tmux server restart dropped user-options)
// fall back to the legacy semantics: broker user, cwd=pane_current_path.
func (r *TmuxRuntime) Exec(ctx context.Context, id string, cmd []string) (string, error) {
	if id == "" {
		return "", fmt.Errorf("tmux runtime: Exec requires a non-empty id")
	}
	if len(cmd) == 0 {
		return "", fmt.Errorf("tmux runtime: Exec requires a non-empty command")
	}
	id = r.resolveTarget(ctx, id)
	if isTmuxBinary(cmd[0]) {
		if len(cmd) > 1 && cmd[1] == "has-session" {
			if r.windowExists(ctx, id) {
				return "", nil
			}
			return "", fmt.Errorf("tmux runtime: agent window %s not found", id)
		}
		effectiveTarget := id
		if needsPaneTarget(cmd[1:]) {
			effectiveTarget = r.resolvePaneTarget(ctx, id)
		}
		rewritten := rewriteTmuxTarget(cmd[1:], effectiveTarget)
		out, err := exec.CommandContext(ctx, r.Command, rewritten...).CombinedOutput()
		if err != nil {
			return string(out), fmt.Errorf("tmux %s on %s: %w (output: %s)",
				strings.Join(rewritten, " "), id, err, strings.TrimSpace(string(out)))
		}
		return string(out), nil
	}
	if !r.windowExists(ctx, id) {
		return "", fmt.Errorf("tmux runtime: agent window %s not found", id)
	}
	return r.execAsAgent(ctx, id, cmd)
}

// execAsAgent runs a host command impersonating the agent: cwd is the
// agent's workspace, HOME is the agent home recorded at start in the
// @scion-home window option, and the SCION_* identity variables mirror
// what Run exports. Resolved secrets are deliberately not replayed —
// they were materialized on disk under the agent home at start, so
// commands read them exactly the way the agent does. When no home is
// recorded the command degrades to execWithPaneCwd rather than failing,
// keeping exec working against windows started before this option existed.
func (r *TmuxRuntime) execAsAgent(ctx context.Context, id string, cmd []string) (string, error) {
	meta, err := r.readMetadata(ctx, id)
	if err != nil || meta.home == "" {
		return r.execWithPaneCwd(ctx, id, cmd)
	}
	workdir := meta.workspace
	if workdir == "" {
		workdir = meta.home
	}
	env := append(os.Environ(),
		"HOME="+meta.home,
		"SCION_AGENT_HOME="+meta.home,
	)
	if name := meta.labels["scion.name"]; name != "" {
		env = append(env, "SCION_AGENT="+name)
	}
	if project := projectcompat.ProjectNameFromLabels(meta.labels); project != "" {
		env = append(env, "SCION_PROJECT="+project, "SCION_GROVE="+project)
	}
	if projectID := projectcompat.ProjectIDFromLabels(meta.labels); projectID != "" {
		env = append(env, "SCION_PROJECT_ID="+projectID, "SCION_GROVE_ID="+projectID)
	}
	c := exec.CommandContext(ctx, cmd[0], cmd[1:]...)
	c.Dir = workdir
	c.Env = env
	out, runErr := c.CombinedOutput()
	if runErr != nil {
		return string(out), fmt.Errorf("exec %v on %s (cwd=%s, home=%s): %w (output: %s)",
			cmd, id, workdir, meta.home, runErr, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// execWithPaneCwd is the legacy Exec semantics: run as the broker user with
// cwd taken from the window's live pane. Kept as the fallback for windows
// with no recorded @scion-home.
func (r *TmuxRuntime) execWithPaneCwd(ctx context.Context, id string, cmd []string) (string, error) {
	workdir, err := r.readPaneCwd(ctx, id)
	if err != nil {
		workdir = ""
	}
	c := exec.CommandContext(ctx, cmd[0], cmd[1:]...)
	c.Dir = workdir
	out, runErr := c.CombinedOutput()
	if runErr != nil {
		return string(out), fmt.Errorf("exec %v on %s (cwd=%s): %w (output: %s)",
			cmd, id, workdir, runErr, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func (r *TmuxRuntime) readPaneCwd(ctx context.Context, id string) (string, error) {
	out, err := exec.CommandContext(ctx, r.Command,
		"display-message", "-p", "-t", id, "#{pane_current_path}").Output()
	if err != nil {
		return "", fmt.Errorf("tmux display-message #{pane_current_path} -t %s: %w", id, err)
	}
	return strings.TrimSpace(string(out)), nil
}

func isTmuxBinary(name string) bool {
	return name == "tmux" || strings.HasSuffix(name, "/tmux")
}

// rewriteTmuxTarget replaces every `-t <X>` pair with `-t <target>`.
func rewriteTmuxTarget(args []string, target string) []string {
	out := make([]string, 0, len(args))
	skipNext := false
	for i, a := range args {
		if skipNext {
			skipNext = false
			continue
		}
		if a == "-t" && i+1 < len(args) {
			out = append(out, "-t", target)
			skipNext = true
			continue
		}
		out = append(out, a)
	}
	return out
}

func (r *TmuxRuntime) GetWorkspacePath(ctx context.Context, id string) (string, error) {
	if id == "" {
		return "", fmt.Errorf("tmux runtime: GetWorkspacePath requires a non-empty id")
	}
	id = r.resolveTarget(ctx, id)
	out, err := exec.CommandContext(ctx, r.Command,
		"show-option", "-w", "-v", "-t", id, tmuxWorkspaceOption).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("tmux show-option %s -t %s: %w (output: %s)",
			tmuxWorkspaceOption, id, err, strings.TrimSpace(string(out)))
	}
	workspace := strings.TrimSpace(string(out))
	if workspace == "" {
		return "", fmt.Errorf("tmux runtime: window %s has no %s set", id, tmuxWorkspaceOption)
	}
	return workspace, nil
}

// formatWindowTarget joins a tmux session and window id into the
// "<session>:<window_id>" target string used everywhere downstream.
// Free function (not a method) because per-agent session can differ
// from r.Session — callers always pass the resolved session.
func formatWindowTarget(session, windowID string) string {
	return fmt.Sprintf("%s:%s", session, windowID)
}

// warnIgnoredFeatures emits stderr warnings for RunConfig fields the tmux
// runtime silently drops. Volumes have no meaning here — the host filesystem
// is already visible to the agent at its real paths.
func warnIgnoredFeatures(config RunConfig) {
	if n := len(config.Volumes); n > 0 {
		fmt.Fprintf(os.Stderr, "Warning: tmux runtime ignores %d volume mount(s); host filesystem is already visible to the agent\n", n)
	}
}

// cloneWorkspaceIfRequested performs the git clone that sciontool would do
// inside a container, since tmux runtime runs no init wrapper. Skipped when
// no clone is configured or the workspace already contains a checkout.
func cloneWorkspaceIfRequested(ctx context.Context, config RunConfig) error {
	if config.GitClone == nil || config.Workspace == "" {
		return nil
	}
	if _, err := os.Stat(filepath.Join(config.Workspace, ".git")); err == nil {
		return nil
	}
	if err := os.MkdirAll(config.Workspace, 0o755); err != nil {
		return fmt.Errorf("tmux runtime: create workspace dir: %w", err)
	}
	token := os.Getenv("GITHUB_TOKEN")
	args := buildGitCloneArgs(config.GitClone, config.Workspace, token)
	out, err := exec.CommandContext(ctx, "git", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("tmux runtime: git clone failed: %w (output: %s)",
			err, sanitizeGitOutput(string(out), token))
	}
	return nil
}

// buildGitCloneArgs is the pure half of cloneWorkspaceIfRequested. Token is
// woven into the URL via https://oauth2:<token>@ when present; never logged.
func buildGitCloneArgs(gc *api.GitCloneConfig, workspace, token string) []string {
	branch := gc.Branch
	if branch == "" {
		branch = "main"
	}
	depth := gc.Depth
	if depth <= 0 {
		depth = 1
	}
	url := gc.URL
	if token != "" {
		url = injectGitToken(url, token)
	}
	return []string{"clone", "--depth", fmt.Sprintf("%d", depth), "--branch", branch, url, workspace}
}

// injectGitToken weaves an OAuth token into an HTTPS git URL. Non-HTTPS URLs
// pass through unchanged (token would not be applicable).
func injectGitToken(rawURL, token string) string {
	const httpsPrefix = "https://"
	if !strings.HasPrefix(rawURL, httpsPrefix) {
		return rawURL
	}
	return httpsPrefix + "oauth2:" + token + "@" + rawURL[len(httpsPrefix):]
}

// sanitizeGitOutput redacts the token from git stderr before surfacing it.
func sanitizeGitOutput(s, token string) string {
	if token == "" {
		return strings.TrimSpace(s)
	}
	return strings.TrimSpace(strings.ReplaceAll(s, token, "***"))
}

var _ Runtime = (*TmuxRuntime)(nil)
