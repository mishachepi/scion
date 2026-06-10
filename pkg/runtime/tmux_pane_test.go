// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package runtime

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeTmuxWithPaneOption: show-option for @scion-pane returns the supplied
// paneID; everything else (display-message #{pane_id}, set-option, send-keys)
// no-ops. Lets us assert that send-keys routes through the persisted pane_id
// when one is set.
func fakeTmuxWithPaneOption(t *testing.T, tmpDir, logPath, paneID string) string {
	t.Helper()
	// Use echo (not printf) for paneID — printf treats '%' as a format
	// spec and would mangle '%42' into nothing.
	script := `#!/bin/sh
printf '%s\n' "$*" >> "` + logPath + `"
case "$1" in
  show-option)
    if [ "$6" = "@scion-pane" ]; then echo '` + paneID + `'; fi
    exit 0 ;;
  display-message)
    echo '` + paneID + `'; exit 0 ;;
  *) exit 0 ;;
esac
`
	path := filepath.Join(tmpDir, "fake-tmux-pane")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake-tmux-pane: %v", err)
	}
	return path
}

func TestTmuxRuntime_Exec_RoutesSendKeysToPaneId(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "tmux.log")
	r := &TmuxRuntime{
		Command: fakeTmuxWithPaneOption(t, tmpDir, logPath, "%42"),
		Session: "scion",
	}

	_, err := r.Exec(context.Background(), "scion:@5",
		[]string{"tmux", "send-keys", "-t", "scion:0", "hello"})
	if err != nil {
		t.Fatalf("Exec send-keys: %v", err)
	}

	log := readLog(t, logPath)
	if !strings.Contains(log, "send-keys -t %42 hello") {
		t.Errorf("send-keys should target pane %%42 (from @scion-pane); log:\n%s", log)
	}
}

func TestTmuxRuntime_Exec_FallsBackToWindowWhenPaneOptionMissing(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "tmux.log")
	// show-option returns empty — no @scion-pane set
	script := `#!/bin/sh
printf '%s\n' "$*" >> "` + logPath + `"
exit 0
`
	path := filepath.Join(tmpDir, "fake-tmux-no-pane")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake-tmux-no-pane: %v", err)
	}
	r := &TmuxRuntime{Command: path, Session: "scion"}

	_, err := r.Exec(context.Background(), "scion:@5",
		[]string{"tmux", "send-keys", "-t", "scion:0", "hello"})
	if err != nil {
		t.Fatalf("Exec send-keys: %v", err)
	}

	log := readLog(t, logPath)
	if !strings.Contains(log, "send-keys -t scion:@5 hello") {
		t.Errorf("send-keys must fall back to window target when @scion-pane is unset; log:\n%s", log)
	}
}

func TestTmuxRuntime_Exec_FallsBackOnMalformedPaneId(t *testing.T) {
	// show-option returns a string that doesn't look like a pane_id (no '%' prefix).
	// Real-world scenario: option set to garbage or leftover by a buggy older
	// version. Must fall back to window target instead of using garbage.
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "tmux.log")
	script := `#!/bin/sh
printf '%s\n' "$*" >> "` + logPath + `"
case "$1" in
  show-option) printf 'not-a-pane-id\n'; exit 0 ;;
  *) exit 0 ;;
esac
`
	path := filepath.Join(tmpDir, "fake-tmux-garbage")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake-tmux-garbage: %v", err)
	}
	r := &TmuxRuntime{Command: path, Session: "scion"}

	_, err := r.Exec(context.Background(), "scion:@5",
		[]string{"tmux", "send-keys", "-t", "scion:0", "hello"})
	if err != nil {
		t.Fatalf("Exec send-keys: %v", err)
	}

	log := readLog(t, logPath)
	if strings.Contains(log, "send-keys -t not-a-pane-id") {
		t.Errorf("garbage @scion-pane must not be used as a target; log:\n%s", log)
	}
	if !strings.Contains(log, "send-keys -t scion:@5 hello") {
		t.Errorf("send-keys must fall back to window target; log:\n%s", log)
	}
}

func TestTmuxRuntime_Run_PersistsPaneIdAtAgentStart(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "tmux.log")
	// new-window prints @5, display-message #{pane_id} prints %42
	script := `#!/bin/sh
printf '%s\n' "$*" >> "` + logPath + `"
case "$1" in
  has-session)     exit 0 ;;
  new-window)      echo '@5'; exit 0 ;;
  list-windows)    echo '@5'; exit 0 ;;
  display-message) echo '%42'; exit 0 ;;
  *) exit 0 ;;
esac
`
	path := filepath.Join(tmpDir, "fake-tmux-with-pane")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake-tmux-with-pane: %v", err)
	}
	r := &TmuxRuntime{Command: path, Session: "scion"}

	_, err := r.Run(context.Background(), RunConfig{
		Name:    "agent-1",
		HomeDir: "/tmp/home",
		Harness: &MockHarness{},
		Labels:  map[string]string{"scion.name": "agent-1"},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	log := readLog(t, logPath)
	if !strings.Contains(log, "display-message -p -t scion:@5 #{pane_id}") {
		t.Errorf("Run must query #{pane_id} after new-window; log:\n%s", log)
	}
	if !strings.Contains(log, "set-option -w -t scion:@5 @scion-pane %42") {
		t.Errorf("Run must persist pane_id to @scion-pane; log:\n%s", log)
	}
}

func TestNeedsPaneTarget(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want bool
	}{
		{nil, false},
		{[]string{}, false},
		{[]string{"send-keys", "-t", "x", "hi"}, true},
		{[]string{"paste-buffer", "-t", "x", "-p"}, true},
		{[]string{"display-message", "-p", "-t", "x", "#{pane_id}"}, true},
		{[]string{"capture-pane", "-t", "x"}, true},
		{[]string{"set-option", "-w", "-t", "x", "@k", "v"}, false},
		{[]string{"kill-window", "-t", "x"}, false},
		{[]string{"list-windows"}, false},
	} {
		got := needsPaneTarget(tc.args)
		if got != tc.want {
			t.Errorf("needsPaneTarget(%v) = %v, want %v", tc.args, got, tc.want)
		}
	}
}
