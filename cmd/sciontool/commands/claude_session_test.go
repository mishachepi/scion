/*
Copyright 2025 The Scion Authors.
*/

package commands

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestEscapeClaudeProjectDir(t *testing.T) {
	tests := []struct {
		path     string
		expected string
	}{
		{"/Volumes/data", "-Volumes-data"},
		{"/Users/alice/.scion", "-Users-alice--scion"},
		{"/Users/alice/Alt_project", "-Users-alice-Alt-project"},
		{"/tmp/a b", "-tmp-a-b"},
	}
	for _, tt := range tests {
		if got := escapeClaudeProjectDir(tt.path); got != tt.expected {
			t.Errorf("escapeClaudeProjectDir(%q) = %q, want %q", tt.path, got, tt.expected)
		}
	}
}

func TestLatestClaudeSessionID(t *testing.T) {
	home := t.TempDir()
	workDir := "/Volumes/data"
	projDir := filepath.Join(home, ".claude", "projects", "-Volumes-data")
	if err := os.MkdirAll(projDir, 0755); err != nil {
		t.Fatal(err)
	}

	write := func(name string, mod time.Time) {
		p := filepath.Join(projDir, name)
		if err := os.WriteFile(p, []byte("{}"), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, mod, mod); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now()
	write("older-session.jsonl", now.Add(-2*time.Hour))
	write("newest-session.jsonl", now)
	write("not-a-session.txt", now.Add(time.Hour))

	if got := latestClaudeSessionID(home, workDir); got != "newest-session" {
		t.Errorf("latestClaudeSessionID = %q, want %q", got, "newest-session")
	}
}

func TestLatestClaudeSessionID_NoSessions(t *testing.T) {
	home := t.TempDir()
	if got := latestClaudeSessionID(home, "/Volumes/data"); got != "" {
		t.Errorf("expected empty id for missing project dir, got %q", got)
	}
}
