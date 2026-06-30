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
	"embed"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/GoogleCloudPlatform/scion/pkg/api"
)

func TestTmuxRuntime_Defaults(t *testing.T) {
	r := NewTmuxRuntime()
	if r.Name() != "tmux" {
		t.Errorf("Name() = %q, want %q", r.Name(), "tmux")
	}
	if r.Command != "tmux" {
		t.Errorf("Command = %q, want %q", r.Command, "tmux")
	}
	if r.Session != DefaultTmuxSession {
		t.Errorf("Session = %q, want %q", r.Session, DefaultTmuxSession)
	}
}

// stubHarness satisfies api.Harness; only Name and GetCommand carry test signal.
type stubHarness struct{ name string }

func (h *stubHarness) Name() string { return h.name }
func (h *stubHarness) GetCommand(string, bool, string, []string) []string {
	return []string{h.name}
}
func (h *stubHarness) GetEnv(string, string, string) map[string]string { return nil }
func (h *stubHarness) DefaultConfigDir() string                        { return "." + h.name }
func (h *stubHarness) SkillsDir() string                               { return "" }
func (h *stubHarness) HasSystemPrompt(string) bool                     { return false }
func (h *stubHarness) Provision(context.Context, string, string, string, string) error {
	return nil
}
func (h *stubHarness) GetEmbedDir() string                          { return h.name }
func (h *stubHarness) GetInterruptKey() string                      { return "Escape" }
func (h *stubHarness) GetInterruptSequence() []string               { return nil }
func (h *stubHarness) GetHarnessEmbedsFS() (embed.FS, string)       { return embed.FS{}, "" }
func (h *stubHarness) InjectAgentInstructions(string, []byte) error { return nil }
func (h *stubHarness) InjectSystemPrompt(string, []byte) error      { return nil }
func (h *stubHarness) GetTelemetryEnv() map[string]string           { return nil }
func (h *stubHarness) ResolveAuth(api.AuthConfig) (*api.ResolvedAuth, error) {
	return &api.ResolvedAuth{Method: "manual"}, nil
}
func (h *stubHarness) AdvancedCapabilities() api.HarnessAdvancedCapabilities {
	return api.HarnessAdvancedCapabilities{Harness: h.name}
}

func TestTmuxRuntime_BuildEnvFlags(t *testing.T) {
	r := NewTmuxRuntime()
	flags := r.buildEnvFlags(RunConfig{
		HomeDir:   "/h",
		Name:      "agent-1",
		Project:   "myproj",
		ProjectID: "id-7",
		Env:       []string{"FOO=bar", ""},
		ResolvedSecrets: []api.ResolvedSecret{
			{Type: "environment", Target: "ANTHROPIC_API_KEY", Value: "sk-test"},
			{Type: "file", Target: "/ignored", Value: "ignored"},
			{Type: "environment", Target: "", Value: "dropped"},
		},
	})

	for _, want := range []string{
		"HOME=/h",
		"SCION_AGENT=agent-1",
		"SCION_AGENT_HOME=/h",
		"SCION_PROJECT=myproj",
		"SCION_GROVE=myproj",
		"SCION_PROJECT_ID=id-7",
		"SCION_GROVE_ID=id-7",
		"FOO=bar",
		"ANTHROPIC_API_KEY=sk-test",
	} {
		if !slices.Contains(flags, want) {
			t.Errorf("flags missing %q: %v", want, flags)
		}
	}
	for _, kv := range flags {
		if kv == "" {
			t.Errorf("empty KV slipped into flags: %v", flags)
		}
		if strings.Contains(kv, "ignored") || strings.Contains(kv, "dropped") {
			t.Errorf("non-environment secret leaked: %q", kv)
		}
	}
	// -e must precede each KV — the alternation contract.
	for i := 0; i < len(flags); i += 2 {
		if flags[i] != "-e" {
			t.Errorf("flags[%d] = %q, want %q (alternation broken)", i, flags[i], "-e")
		}
	}
}

func TestTmuxRuntime_BuildNewWindowArgs_LayoutAndOrder(t *testing.T) {
	r := &TmuxRuntime{Command: "tmux", Session: "scion"}
	args, err := r.buildNewWindowArgs(RunConfig{
		Name:      "agent-1",
		HomeDir:   "/tmp/home",
		Workspace: "/tmp/ws",
		Project:   "myproj",
		Env:       []string{"FOO=bar"},
		Harness:   &MockHarness{},
		Task:      "do thing",
	})
	if err != nil {
		t.Fatalf("buildNewWindowArgs: %v", err)
	}

	want := []string{"new-window", "-d", "-t", "scion", "-n", "agent-1", "-c", "/tmp/ws"}
	if !slices.Equal(args[:len(want)], want) {
		t.Errorf("positional prefix = %v, want %v", args[:len(want)], want)
	}

	// -P -F #{window_id} must follow env flags and precede the harness command.
	pIdx, fIdx := indexOf(args, "-P"), indexOf(args, "#{window_id}")
	if pIdx < 0 || fIdx <= pIdx {
		t.Errorf("-P / #{window_id} ordering broken: %v", args)
	}
	echoIdx := indexOf(args, "/bin/echo")
	if echoIdx < 0 || echoIdx != fIdx+1 {
		t.Errorf("harness command should follow -P -F #{window_id}; args=%v", args)
	}
}

func TestTmuxRuntime_BuildNewWindowArgs_Validation(t *testing.T) {
	r := &TmuxRuntime{Command: "tmux", Session: "scion"}
	cases := []struct {
		name    string
		config  RunConfig
		wantErr string
	}{
		{"no harness", RunConfig{Name: "a", HomeDir: "/h"}, "no harness"},
		{"no homedir", RunConfig{Name: "a", Harness: &MockHarness{}}, "HomeDir"},
		{"no name", RunConfig{HomeDir: "/h", Harness: &MockHarness{}}, "Name"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := r.buildNewWindowArgs(tc.config)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("err = %v, want substring %q", err, tc.wantErr)
			}
		})
	}
}

func indexOf(args []string, want string) int {
	for i, a := range args {
		if a == want {
			return i
		}
	}
	return -1
}

// fakeTmux: succeeds for has-session; new-window prints "@5"; list-windows lists "@5".
func fakeTmux(t *testing.T, tmpDir, logPath string) string {
	t.Helper()
	script := `#!/bin/sh
printf '%s\n' "$*" >> "` + logPath + `"
case "$1" in
  has-session)  exit 0 ;;
  new-window)   printf '@5\n'; exit 0 ;;
  list-windows) printf '@5\n'; exit 0 ;;
  *) exit 0 ;;
esac
`
	path := filepath.Join(tmpDir, "fake-tmux")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake-tmux: %v", err)
	}
	return path
}

// fakeTmuxMissingWindow: list-windows returns empty; kill-window fails with the
// "can't find window" marker.
func fakeTmuxMissingWindow(t *testing.T, tmpDir, logPath string) string {
	t.Helper()
	script := `#!/bin/sh
printf '%s\n' "$*" >> "` + logPath + `"
case "$1" in
  list-windows) exit 0 ;;
  kill-window)  printf "can't find window: @5\n" >&2; exit 1 ;;
  *) exit 0 ;;
esac
`
	path := filepath.Join(tmpDir, "fake-tmux-missing")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake-tmux-missing: %v", err)
	}
	return path
}

func readLog(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read log %s: %v", path, err)
	}
	return string(b)
}

func TestTmuxRuntime_Run_HappyPath(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "tmux.log")
	r := &TmuxRuntime{Command: fakeTmux(t, tmpDir, logPath), Session: "scion"}

	target, err := r.Run(context.Background(), RunConfig{
		Name:      "agent-1",
		HomeDir:   "/tmp/home",
		Workspace: "/tmp/ws",
		Project:   "myproj",
		Labels: map[string]string{
			"scion.name":     "agent-1",
			"scion.template": "web-dev",
			"team":           "backend",
		},
		Annotations: map[string]string{"scion.project_path": "/tmp/myproj"},
		Harness:     &MockHarness{},
		Task:        "do thing",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if target != "scion:@5" {
		t.Errorf("target = %q, want %q", target, "scion:@5")
	}

	log := readLog(t, logPath)
	for _, want := range []string{
		"new-window -d -t scion -n agent-1",
		"-c /tmp/ws",
		"-e SCION_AGENT=agent-1",
		"-P -F #{window_id}",
		"set-option -w -t scion:@5 @scion-label-scion.name agent-1",
		"set-option -w -t scion:@5 @scion-label-team backend",
		"set-option -w -t scion:@5 @scion-annotation-scion.project_path /tmp/myproj",
		"set-option -w -t scion:@5 @scion-workspace /tmp/ws",
	} {
		if !strings.Contains(log, want) {
			t.Errorf("tmux call log missing %q\n---log---\n%s", want, log)
		}
	}
}

func TestTmuxRuntime_Run_RejectsDuplicateScionName(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "tmux.log")

	r := &TmuxRuntime{
		Command: fakeTmuxList(t, tmpDir, logPath,
			[]fakeWindow{{id: "@5", name: "agent-1"}},
			map[string]map[string]string{
				"scion:@5": {"@scion-label-scion.name": "agent-1"},
			},
			nil),
		Session: "scion",
	}

	_, err := r.Run(context.Background(), RunConfig{
		Name:    "agent-1",
		HomeDir: "/tmp/home",
		Harness: &MockHarness{},
		Labels:  map[string]string{"scion.name": "agent-1"},
	})
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("err = %v, want 'already exists'", err)
	}
	if !strings.Contains(err.Error(), "scion:@5") {
		t.Errorf("err = %v, want it to surface the conflicting target", err)
	}
	if strings.Contains(readLog(t, logPath), "new-window") {
		t.Errorf("new-window should not be called on collision")
	}
}

func TestTmuxRuntime_Stop_HappyPath_SendsInterruptThenKill(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "tmux.log")
	r := &TmuxRuntime{Command: fakeTmux(t, tmpDir, logPath), Session: "scion"}

	if err := r.Stop(context.Background(), "scion:@5"); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	log := readLog(t, logPath)

	for _, want := range []string{
		"list-windows -t scion -F #{window_id}",
		"send-keys -t scion:@5 C-c",
		"kill-window -t scion:@5",
	} {
		if !strings.Contains(log, want) {
			t.Errorf("tmux call log missing %q\n---log---\n%s", want, log)
		}
	}

	sendIdx := strings.Index(log, "send-keys -t scion:@5 C-c")
	killIdx := strings.Index(log, "kill-window -t scion:@5")
	if sendIdx >= killIdx {
		t.Errorf("expected send-keys to precede kill-window; sendIdx=%d killIdx=%d", sendIdx, killIdx)
	}
}

func TestTmuxRuntime_Stop_IdempotentWhenWindowAlreadyGone(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "tmux.log")
	r := &TmuxRuntime{Command: fakeTmuxMissingWindow(t, tmpDir, logPath), Session: "scion"}

	if err := r.Stop(context.Background(), "scion:@99"); err != nil {
		t.Errorf("Stop on missing window should be idempotent; got err=%v", err)
	}
	log := readLog(t, logPath)
	if strings.Contains(log, "send-keys -t scion:@99") || strings.Contains(log, "kill-window -t scion:@99") {
		t.Errorf("send-keys/kill-window should not run on missing window:\n%s", log)
	}
}

func TestTmuxRuntime_Delete_HappyPath_KillsWithoutInterrupt(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "tmux.log")
	r := &TmuxRuntime{Command: fakeTmux(t, tmpDir, logPath), Session: "scion"}

	if err := r.Delete(context.Background(), "scion:@5"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	log := readLog(t, logPath)
	if !strings.Contains(log, "kill-window -t scion:@5") {
		t.Errorf("expected kill-window in log:\n%s", log)
	}
	if strings.Contains(log, "send-keys -t scion:@5 C-c") {
		t.Errorf("Delete must not send Ctrl-C; log:\n%s", log)
	}
}

// fakeTmuxList simulates a tmux server with a fixed window list and per-target
// user-options.
func fakeTmuxList(t *testing.T, tmpDir, logPath string,
	windows []fakeWindow, optionsByTarget map[string]map[string]string,
	missingTargets map[string]bool) string {
	t.Helper()

	// The production List() now invokes `list-windows -a -F
	// '#{session_name}:#{window_id}'` (across every session on the tmux
	// server) instead of scoping by -t. Mirror that in the fake by
	// prefixing each fixture window with the runtime's session name so
	// the resulting targets match the show-options lookup keys
	// ("scion:@5", "obsi:@9", ...).
	defaultSession := "scion"
	var listLines strings.Builder
	for _, w := range windows {
		session := w.session
		if session == "" {
			session = defaultSession
		}
		listLines.WriteString(session)
		listLines.WriteString(":")
		listLines.WriteString(w.id)
		listLines.WriteString("\n")
	}

	var showCases strings.Builder
	for target, opts := range optionsByTarget {
		showCases.WriteString("  '" + target + "')\n")
		showCases.WriteString("    cat <<'OPTS'\n")
		showCases.WriteString("status off\n")
		for k, v := range opts {
			showCases.WriteString(k + " \"" + v + "\"\n")
		}
		showCases.WriteString("OPTS\n")
		showCases.WriteString("    exit 0 ;;\n")
	}
	for target := range missingTargets {
		showCases.WriteString("  '" + target + "')\n")
		showCases.WriteString("    printf 'no such window\\n' >&2\n")
		showCases.WriteString("    exit 1 ;;\n")
	}

	script := `#!/bin/sh
printf '%s\n' "$*" >> "` + logPath + `"
case "$1" in
  has-session) exit 0 ;;
  list-windows)
    cat <<'WINDOWS'
` + listLines.String() + `WINDOWS
    exit 0 ;;
  show-options)
    target=""
    shift
    while [ "$#" -gt 0 ]; do
      case "$1" in
        -t) target="$2"; shift 2 ;;
        *)  shift ;;
      esac
    done
    case "$target" in
` + showCases.String() + `
      *) exit 0 ;;
    esac
    ;;
  *) exit 0 ;;
esac
`
	path := filepath.Join(tmpDir, "fake-tmux-list")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake-tmux-list: %v", err)
	}
	return path
}

type fakeWindow struct {
	id, name string
	// session is the tmux session the window belongs to. Empty defaults
	// to "scion". Lets per-test fixtures place windows in any session.
	session string
}

func TestTmuxRuntime_List_BuildsAgentInfoFromWindows(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "tmux.log")
	r := &TmuxRuntime{
		Command: fakeTmuxList(t, tmpDir, logPath,
			[]fakeWindow{{id: "@5", name: "agent-1"}},
			map[string]map[string]string{
				"scion:@5": {
					"@scion-label-scion.name":              "agent-1",
					"@scion-label-scion.template":          "web-dev",
					"@scion-label-scion.project":           "myproj",
					"@scion-label-scion.harness_config":    "claude-default",
					"@scion-label-team":                    "backend",
					"@scion-annotation-scion.project_path": "/tmp/myproj",
					"@scion-workspace":                     "/tmp/ws-1",
				},
			},
			nil,
		),
		Session: "scion",
	}

	agents, err := r.List(context.Background(), nil)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(agents) != 1 {
		t.Fatalf("len(agents) = %d, want 1; agents=%+v", len(agents), agents)
	}
	a := agents[0]
	if a.Name != "agent-1" || a.Template != "web-dev" || a.Project != "myproj" ||
		a.HarnessConfig != "claude-default" || a.ProjectPath != "/tmp/myproj" ||
		a.Runtime != "tmux" || a.Phase != "running" {
		t.Errorf("agent fields mismatch: %+v", a)
	}
	if a.Labels["team"] != "backend" {
		t.Errorf("user label 'team' missing or wrong: %v", a.Labels)
	}
}

// TestTmuxRuntime_List_FindsAgentsAcrossSessions makes sure List() picks up
// agents that live in a tmux session other than the runtime's configured
// r.Session — the case that broke browser PTY for the live `obsi`/`scion`
// split. The runtime points at "scion" but the agent runs in "obsi".
func TestTmuxRuntime_List_FindsAgentsAcrossSessions(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "tmux.log")
	r := &TmuxRuntime{
		Command: fakeTmuxList(t, tmpDir, logPath,
			[]fakeWindow{
				{id: "@7", name: "agent-in-other-session", session: "obsi"},
				{id: "@9", name: "non-scion", session: "scion"}, // no labels — filtered
			},
			map[string]map[string]string{
				"obsi:@7": {
					"@scion-label-scion.name":     "core",
					"@scion-label-scion.template": "core",
					"@scion-label-scion.project":  "demo",
				},
				"scion:@9": {}, // no scion.name → skipped
			},
			nil,
		),
		Session: "scion", // explicitly different from the agent's session
	}

	agents, err := r.List(context.Background(), nil)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(agents) != 1 {
		t.Fatalf("len(agents) = %d, want 1; agents=%+v", len(agents), agents)
	}
	a := agents[0]
	if a.ContainerID != "obsi:@7" {
		t.Errorf("ContainerID = %q, want %q", a.ContainerID, "obsi:@7")
	}
	if a.Name != "core" {
		t.Errorf("Name = %q, want %q", a.Name, "core")
	}
}

// TestTmuxRuntime_List_NoTmuxServer makes sure List() returns no agents (and
// no error) when there's no tmux server running at all — `list-windows -a`
// exits non-zero in that case. Mirrors the prior r.sessionExists() guard
// which silently produced an empty slice.
func TestTmuxRuntime_List_NoTmuxServer(t *testing.T) {
	tmpDir := t.TempDir()
	// fake tmux that always exits 1 — simulates "no server running".
	fakeBin := filepath.Join(tmpDir, "fake-tmux-no-server")
	if err := os.WriteFile(fakeBin, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatalf("write fake-tmux-no-server: %v", err)
	}
	r := &TmuxRuntime{Command: fakeBin, Session: "scion"}

	agents, err := r.List(context.Background(), nil)
	if err != nil {
		t.Errorf("List returned error for absent tmux server: %v", err)
	}
	if len(agents) != 0 {
		t.Errorf("expected no agents when tmux server is absent, got %+v", agents)
	}
}

func TestTmuxRuntime_List_SkipsWindowsWithoutScionName(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "tmux.log")
	r := &TmuxRuntime{
		Command: fakeTmuxList(t, tmpDir, logPath,
			[]fakeWindow{{id: "@5", name: "external-window"}},
			map[string]map[string]string{"scion:@5": {}},
			nil,
		),
		Session: "scion",
	}
	agents, err := r.List(context.Background(), nil)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(agents) != 0 {
		t.Errorf("non-scion windows must be filtered out; got %+v", agents)
	}
}

// fakeTmuxLogsAndOptions: capture-pane returns canned scrollback; show-option
// -v returns the supplied value for the matching option name.
func fakeTmuxLogsAndOptions(t *testing.T, tmpDir string,
	captureOutput string, optionValues map[string]string) string {
	t.Helper()

	var optCases strings.Builder
	for opt, val := range optionValues {
		optCases.WriteString("    '" + opt + "') printf '%s\\n' '" + val + "'; exit 0 ;;\n")
	}

	script := `#!/bin/sh
case "$1" in
  capture-pane)
    cat <<'PANE'
` + captureOutput + `PANE
    exit 0 ;;
  show-option)
    option="${@: -1}"
    case "$option" in
` + optCases.String() + `
      *) printf ''; exit 0 ;;
    esac
    ;;
  *) exit 0 ;;
esac
`
	path := filepath.Join(tmpDir, "fake-tmux-logs-opts")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake-tmux-logs-opts: %v", err)
	}
	return path
}

func TestTmuxRuntime_GetLogs_ReturnsScrollback(t *testing.T) {
	tmpDir := t.TempDir()
	want := "line 1\nline 2\nline 3\n"
	r := &TmuxRuntime{Command: fakeTmuxLogsAndOptions(t, tmpDir, want, nil), Session: "scion"}

	got, err := r.GetLogs(context.Background(), "scion:@5")
	if err != nil {
		t.Fatalf("GetLogs: %v", err)
	}
	if got != want {
		t.Errorf("GetLogs = %q, want %q", got, want)
	}
}

func TestTmuxRuntime_GetWorkspacePath_ReadsDedicatedOption(t *testing.T) {
	tmpDir := t.TempDir()
	r := &TmuxRuntime{
		Command: fakeTmuxLogsAndOptions(t, tmpDir, "",
			map[string]string{"@scion-workspace": "/tmp/ws"}),
		Session: "scion",
	}
	got, err := r.GetWorkspacePath(context.Background(), "scion:@5")
	if err != nil {
		t.Fatalf("GetWorkspacePath: %v", err)
	}
	if got != "/tmp/ws" {
		t.Errorf("GetWorkspacePath = %q, want %q", got, "/tmp/ws")
	}
}

// fakeTmuxExec: records each invocation; for tmux subcommand passthrough writes
// the marker to stdout.
func fakeTmuxExec(t *testing.T, tmpDir, logPath string) string {
	t.Helper()
	script := `#!/bin/sh
printf '%s\n' "$*" >> "` + logPath + `"
printf 'tmux-exec-ok\n'
exit 0
`
	path := filepath.Join(tmpDir, "fake-tmux-exec")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake-tmux-exec: %v", err)
	}
	return path
}

// fakeTmuxExecWithCwd: list-windows returns "@5"; display-message returns the
// supplied cwd.
func fakeTmuxExecWithCwd(t *testing.T, tmpDir, logPath, cwd string) string {
	t.Helper()
	script := `#!/bin/sh
printf '%s\n' "$*" >> "` + logPath + `"
case "$1" in
  list-windows)    printf '@5\n'; exit 0 ;;
  display-message) printf '%s\n' '` + cwd + `'; exit 0 ;;
  *) printf 'tmux-exec-ok\n'; exit 0 ;;
esac
`
	path := filepath.Join(tmpDir, "fake-tmux-exec-cwd")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake-tmux-exec-cwd: %v", err)
	}
	return path
}

func TestTmuxRuntime_Exec_RunsHostCommandAtPaneCwd(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "tmux.log")
	r := &TmuxRuntime{
		Command: fakeTmuxExecWithCwd(t, tmpDir, logPath, tmpDir),
		Session: "scion",
	}

	out, err := r.Exec(context.Background(), "scion:@5", []string{"/bin/pwd"})
	if err != nil {
		t.Fatalf("Exec /bin/pwd: %v", err)
	}
	if strings.TrimSpace(out) != tmpDir {
		t.Errorf("/bin/pwd via Exec = %q, want %q (agent cwd)", strings.TrimSpace(out), tmpDir)
	}
}

func TestTmuxRuntime_Exec_RewritesTargetForTmuxPassthrough(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "tmux.log")
	r := &TmuxRuntime{Command: fakeTmuxExec(t, tmpDir, logPath), Session: "scion"}

	if _, err := r.Exec(context.Background(), "scion:@5",
		[]string{"tmux", "send-keys", "-t", "scion:0", "hello"}); err != nil {
		t.Fatalf("Exec: %v", err)
	}

	log := readLog(t, logPath)
	if !strings.Contains(log, "send-keys -t scion:@5 hello") {
		t.Errorf("expected -t rewritten to scion:@5; log:\n%s", log)
	}
	if strings.Contains(log, "send-keys -t scion:0 hello") {
		t.Errorf("original -t target should have been rewritten; log:\n%s", log)
	}
}

// scion's waitForTmuxSession sends `tmux has-session -t scion` via Exec to ask
// "agent ready yet?". For the tmux runtime the agent IS the host window, so
// the probe must map to windowExists rather than forward to host tmux.
func TestTmuxRuntime_Exec_HasSessionMapsToWindowExists(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "tmux.log")
	r := &TmuxRuntime{Command: fakeTmux(t, tmpDir, logPath), Session: "scion"}

	if _, err := r.Exec(context.Background(), "scion:@5",
		[]string{"tmux", "has-session", "-t", "scion"}); err != nil {
		t.Fatalf("has-session probe should succeed when window exists; err=%v", err)
	}

	log := readLog(t, logPath)
	if !strings.Contains(log, "list-windows -t scion -F #{window_id}") {
		t.Errorf("expected list-windows probe in log:\n%s", log)
	}
	if strings.Contains(log, "has-session") {
		t.Errorf("has-session should not be forwarded to host tmux:\n%s", log)
	}
}

func TestValidateTmuxSessionName(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		wantErr string
	}{
		{"default", "scion", ""},
		{"with dash", "lsa-vault", ""},
		{"empty", "", "must not be empty"},
		{"contains colon", "bad:name", "':' or '.'"},
		{"contains dot", "bad.name", "':' or '.'"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateTmuxSessionName(tc.input)
			if tc.wantErr == "" {
				if err != nil {
					t.Errorf("ValidateTmuxSessionName(%q) = %v, want nil", tc.input, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("ValidateTmuxSessionName(%q) = %v, want substring %q", tc.input, err, tc.wantErr)
			}
		})
	}
}

// Regression guard: @1 must NOT match @10 / @12 / @123 (exact line compare).
func TestTmuxRuntime_WindowExists_ExactLineMatch(t *testing.T) {
	tmpDir := t.TempDir()
	script := `#!/bin/sh
case "$1" in
  list-windows) printf '@10\n@12\n@123\n'; exit 0 ;;
  *) exit 0 ;;
esac
`
	bin := filepath.Join(tmpDir, "fake-tmux-windowlist")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake: %v", err)
	}
	r := &TmuxRuntime{Command: bin, Session: "scion"}

	if r.windowExists(context.Background(), "scion:@1") {
		t.Errorf("@1 falsely matched against @10/@12/@123")
	}
	if !r.windowExists(context.Background(), "scion:@12") {
		t.Errorf("@12 should match the live window list")
	}
}

func TestTmuxRuntime_RunPreStartScript_HappyPath(t *testing.T) {
	tmp := t.TempDir()
	scriptPath := filepath.Join(tmp, "pre.sh")
	outPath := filepath.Join(tmp, "out.txt")
	body := "#!/bin/sh\nprintf '%s\\n' \"$1\" \"$2\" > " + outPath + "\n"
	if err := os.WriteFile(scriptPath, []byte(body), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}
	r := &TmuxRuntime{PreStartScript: scriptPath}
	if err := r.runPreStartScript(context.Background(), "/tmp/agent-home"); err != nil {
		t.Fatalf("runPreStartScript: %v", err)
	}
	got, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read script output: %v", err)
	}
	want := "/tmp/agent-home\n" + os.Getenv("HOME") + "\n"
	if string(got) != want {
		t.Errorf("script output = %q, want %q", string(got), want)
	}
}

func TestTmuxRuntime_RunPreStartScript_RejectsBadPaths(t *testing.T) {
	cases := []struct {
		name     string
		setup    func(t *testing.T) string
		wantSubs string
	}{
		{"missing file",
			func(t *testing.T) string { return filepath.Join(t.TempDir(), "missing.sh") },
			"no such file"},
		{"directory not file",
			func(t *testing.T) string { return t.TempDir() },
			"is a directory"},
		{"not executable",
			func(t *testing.T) string {
				p := filepath.Join(t.TempDir(), "noexec.sh")
				if err := os.WriteFile(p, []byte("#!/bin/sh\nexit 0\n"), 0o644); err != nil {
					t.Fatalf("write: %v", err)
				}
				return p
			},
			"not executable"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := &TmuxRuntime{PreStartScript: tc.setup(t)}
			err := r.runPreStartScript(context.Background(), "/tmp/agent-home")
			if err == nil || !strings.Contains(err.Error(), tc.wantSubs) {
				t.Errorf("error = %v, want substring %q", err, tc.wantSubs)
			}
		})
	}
}

func TestWarnIgnoredFeatures(t *testing.T) {
	cases := []struct {
		name    string
		config  RunConfig
		wantSub string
	}{
		{"empty config", RunConfig{}, ""},
		{
			"volumes ignored",
			RunConfig{Volumes: []api.VolumeMount{{Source: "/a", Target: "/b"}, {Source: "/c", Target: "/d"}}},
			"2 volume mount",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := captureStderr(t, func() { warnIgnoredFeatures(tc.config) })
			if tc.wantSub == "" {
				if got != "" {
					t.Errorf("expected no warning, got %q", got)
				}
				return
			}
			if !strings.Contains(got, tc.wantSub) {
				t.Errorf("stderr %q missing substring %q", got, tc.wantSub)
			}
		})
	}
}

func TestBuildGitCloneArgs(t *testing.T) {
	intPtr := func(v int) *int { return &v }
	cases := []struct {
		name      string
		gc        api.GitCloneConfig
		token     string
		wantArgs  []string
		wantURLIn string // substring expected in the URL position (second-to-last arg)
	}{
		{
			name:      "defaults: branch=main, depth=1, no token",
			gc:        api.GitCloneConfig{URL: "https://github.com/example/repo.git"},
			token:     "",
			wantArgs:  []string{"clone", "--depth", "1", "--branch", "main", "https://github.com/example/repo.git", "/ws"},
			wantURLIn: "https://github.com/example/repo.git",
		},
		{
			name:      "explicit branch + depth",
			gc:        api.GitCloneConfig{URL: "https://github.com/example/repo.git", Branch: "feat/x", Depth: intPtr(5)},
			token:     "",
			wantArgs:  []string{"clone", "--depth", "5", "--branch", "feat/x", "https://github.com/example/repo.git", "/ws"},
			wantURLIn: "https://github.com/example/repo.git",
		},
		{
			name:      "depth 0 = full clone, no --depth flag",
			gc:        api.GitCloneConfig{URL: "https://github.com/example/repo.git", Depth: intPtr(0)},
			token:     "",
			wantArgs:  []string{"clone", "--branch", "main", "https://github.com/example/repo.git", "/ws"},
			wantURLIn: "https://github.com/example/repo.git",
		},
		{
			name:      "token injected into https URL",
			gc:        api.GitCloneConfig{URL: "https://github.com/example/repo.git"},
			token:     "ghp_secret",
			wantURLIn: "https://oauth2:ghp_secret@github.com/example/repo.git",
		},
		{
			name:      "ssh URL passed through unchanged",
			gc:        api.GitCloneConfig{URL: "git@github.com:example/repo.git"},
			token:     "ghp_secret",
			wantURLIn: "git@github.com:example/repo.git",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := buildGitCloneArgs(&tc.gc, "/ws", tc.token)
			if len(got) < 2 {
				t.Fatalf("expected at least 2 args, got %d: %v", len(got), got)
			}
			if url := got[len(got)-2]; url != tc.wantURLIn {
				t.Errorf("URL arg = %q, want %q", url, tc.wantURLIn)
			}
			if tc.wantArgs != nil && !slices.Equal(got, tc.wantArgs) {
				t.Errorf("args = %v, want %v", got, tc.wantArgs)
			}
		})
	}
}

func TestSanitizeGitOutput(t *testing.T) {
	if got := sanitizeGitOutput("clone https://oauth2:ghp_abc@host/x.git", "ghp_abc"); strings.Contains(got, "ghp_abc") {
		t.Errorf("token leaked: %q", got)
	}
	if got := sanitizeGitOutput("benign output  ", ""); got != "benign output" {
		t.Errorf("no-token case lost trim: %q", got)
	}
}

func TestCloneWorkspaceIfRequested_NilConfig(t *testing.T) {
	if err := cloneWorkspaceIfRequested(context.Background(), RunConfig{}); err != nil {
		t.Errorf("nil GitClone should be no-op, got err: %v", err)
	}
}

func TestCloneWorkspaceIfRequested_AlreadyCloned(t *testing.T) {
	ws := t.TempDir()
	if err := os.MkdirAll(filepath.Join(ws, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	cfg := RunConfig{
		Workspace: ws,
		GitClone:  &api.GitCloneConfig{URL: "https://example.invalid/repo.git"},
	}
	// Should skip clone (and therefore not fail on the unreachable URL).
	if err := cloneWorkspaceIfRequested(context.Background(), cfg); err != nil {
		t.Errorf("pre-existing .git should short-circuit, got err: %v", err)
	}
}

func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stderr = w
	defer func() { os.Stderr = orig }()

	fn()

	_ = w.Close()
	b, _ := io.ReadAll(r)
	return string(b)
}

func TestTmuxRuntime_RunPreStartScript_PropagatesScriptFailure(t *testing.T) {
	tmp := t.TempDir()
	scriptPath := filepath.Join(tmp, "fail.sh")
	body := "#!/bin/sh\necho 'something went wrong' >&2\nexit 7\n"
	if err := os.WriteFile(scriptPath, []byte(body), 0o755); err != nil {
		t.Fatalf("write: %v", err)
	}
	r := &TmuxRuntime{PreStartScript: scriptPath}
	err := r.runPreStartScript(context.Background(), "/tmp/agent-home")
	if err == nil || !strings.Contains(err.Error(), "something went wrong") {
		t.Errorf("error = %v, want script stderr surfaced", err)
	}
}

// home_mode == "" (unset) must behave identically to home_mode == "agent" so
// existing deployments upgrade silently.
func TestTmuxRuntime_BuildEnvFlags_AgentModeDefaultBackCompat(t *testing.T) {
	rEmpty := &TmuxRuntime{}
	rAgent := &TmuxRuntime{HomeMode: HomeModeAgent}
	cfg := RunConfig{HomeDir: "/h", Name: "a"}
	if got, want := rEmpty.buildEnvFlags(cfg), rAgent.buildEnvFlags(cfg); !slices.Equal(got, want) {
		t.Errorf("empty HomeMode flags = %v\nagent HomeMode flags = %v\n(must be identical)", got, want)
	}
}

func TestTmuxRuntime_BuildEnvFlags_ScionAgentHomeAlwaysSet(t *testing.T) {
	for _, mode := range []string{"", HomeModeAgent, HomeModeSystem} {
		t.Run("mode="+mode, func(t *testing.T) {
			r := &TmuxRuntime{HomeMode: mode}
			flags := r.buildEnvFlags(RunConfig{HomeDir: "/agent-home", Name: "a"})
			if !slices.Contains(flags, "SCION_AGENT_HOME=/agent-home") {
				t.Errorf("SCION_AGENT_HOME missing in HomeMode=%q: %v", mode, flags)
			}
		})
	}
}

// SystemMode = absolute minimum. Exactly one -e flag, exactly SCION_AGENT_HOME.
// No HOME, no SCION_AGENT, no SCION_PROJECT, no Env passthrough, no resolved
// secrets, no *_CONFIG_DIR, nothing else. Load-bearing contract for the mode.
func TestTmuxRuntime_BuildEnvFlags_SystemModeOnlyScionAgentHome(t *testing.T) {
	r := &TmuxRuntime{HomeMode: HomeModeSystem}
	flags := r.buildEnvFlags(RunConfig{
		HomeDir:   "/agent-home",
		Name:      "agent-1",
		Project:   "myproj",
		ProjectID: "id-7",
		Env:       []string{"FOO=bar"},
		ResolvedSecrets: []api.ResolvedSecret{
			{Type: "environment", Target: "ANTHROPIC_API_KEY", Value: "sk-test"},
		},
		Harness: &stubHarness{name: "claude"},
	})
	want := []string{"-e", "SCION_AGENT_HOME=/agent-home"}
	if !slices.Equal(flags, want) {
		t.Errorf("system mode buildEnvFlags = %v, want %v (exactly one entry)", flags, want)
	}
}

// SystemMode also suppresses harness env passthrough done in buildNewWindowArgs.
func TestTmuxRuntime_BuildNewWindowArgs_SystemModeSkipsHarnessEnv(t *testing.T) {
	r := &TmuxRuntime{Command: "tmux", Session: "scion", HomeMode: HomeModeSystem}
	args, err := r.buildNewWindowArgs(RunConfig{
		Name:    "agent-1",
		HomeDir: "/agent-home",
		Harness: &MockHarness{},
	})
	if err != nil {
		t.Fatalf("buildNewWindowArgs: %v", err)
	}
	eCount := 0
	for _, a := range args {
		if a == "-e" {
			eCount++
		}
	}
	if eCount != 1 {
		t.Errorf("system mode emitted %d -e flags, want exactly 1 (SCION_AGENT_HOME): args=%v", eCount, args)
	}
}

func TestTmuxRuntime_BuildNewWindowArgs_NoSciontoolWrap_ByDefault(t *testing.T) {
	r := &TmuxRuntime{Command: "tmux", Session: "scion"}
	args, err := r.buildNewWindowArgs(RunConfig{
		Name:    "agent-1",
		HomeDir: "/tmp/home",
		Harness: &MockHarness{},
	})
	if err != nil {
		t.Fatalf("buildNewWindowArgs: %v", err)
	}
	for _, a := range args {
		if a == "--tmuxruntime" {
			t.Errorf("default config must NOT wrap with sciontool; args=%v", args)
		}
	}
}

func TestTmuxRuntime_BuildNewWindowArgs_SciontoolWrap_WhenConfigured(t *testing.T) {
	r := &TmuxRuntime{
		Command:   "tmux",
		Session:   "scion",
		Sciontool: "/usr/local/bin/sciontool",
	}
	args, err := r.buildNewWindowArgs(RunConfig{
		Name:    "agent-1",
		HomeDir: "/tmp/home",
		Harness: &MockHarness{},
	})
	if err != nil {
		t.Fatalf("buildNewWindowArgs: %v", err)
	}
	// Expect ..., "/usr/local/bin/sciontool", "init", "--tmuxruntime", "--", "/bin/echo"
	wrapIdx := indexOf(args, "/usr/local/bin/sciontool")
	if wrapIdx < 0 {
		t.Fatalf("sciontool wrapper not in args: %v", args)
	}
	want := []string{"/usr/local/bin/sciontool", "init", "--tmuxruntime", "--", "/bin/echo"}
	if got := args[wrapIdx : wrapIdx+len(want)]; !slices.Equal(got, want) {
		t.Errorf("wrapper sequence = %v, want %v", got, want)
	}
}

func TestResolveSciontool(t *testing.T) {
	if got := resolveSciontool(""); got != "" {
		t.Errorf("empty input must yield empty (off), got %q", got)
	}
	if got := resolveSciontool("/explicit/sciontool"); got != "/explicit/sciontool" {
		t.Errorf("explicit path passed through, got %q", got)
	}
	// "auto" with no sciontool on PATH must degrade gracefully (empty + warn).
	t.Setenv("PATH", "")
	stderr := captureStderr(t, func() {
		if got := resolveSciontool("auto"); got != "" {
			t.Errorf("auto with missing binary must yield empty, got %q", got)
		}
	})
	if !strings.Contains(stderr, "not found on PATH") {
		t.Errorf("expected PATH-miss warning, got %q", stderr)
	}
}

func TestTmuxRuntime_RunPreStartScript_SkippedInSystemMode(t *testing.T) {
	tmp := t.TempDir()
	scriptPath := filepath.Join(tmp, "should-not-run.sh")
	canary := filepath.Join(tmp, "canary.txt")
	body := "#!/bin/sh\ntouch " + canary + "\nexit 0\n"
	if err := os.WriteFile(scriptPath, []byte(body), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}
	r := &TmuxRuntime{PreStartScript: scriptPath, HomeMode: HomeModeSystem}
	stderr := captureStderr(t, func() {
		if err := r.runPreStartScript(context.Background(), "/tmp/agent-home"); err != nil {
			t.Fatalf("runPreStartScript: %v", err)
		}
	})
	if _, err := os.Stat(canary); err == nil {
		t.Errorf("script must not run in system mode (canary created)")
	}
	if !strings.Contains(stderr, "skipped in home_mode=system") {
		t.Errorf("stderr should warn about skipped script; got %q", stderr)
	}
}
