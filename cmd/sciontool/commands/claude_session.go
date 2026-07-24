/*
Copyright 2025 The Scion Authors.
*/

package commands

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

// escapeClaudeProjectDir converts a workspace path to the directory name
// Claude Code uses under ~/.claude/projects/: every character outside
// [A-Za-z0-9] becomes '-' (e.g. "/Users/mch/.scion" -> "-Users-mch--scion").
func escapeClaudeProjectDir(path string) string {
	var b strings.Builder
	for _, r := range path {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	return b.String()
}

// latestClaudeSessionID returns the id of the most recently modified Claude
// Code session for workDir, read from home's ~/.claude/projects/<escaped>/
// directory (one <session-id>.jsonl per session). Returns "" when no session
// exists. This lets sciontool capture the session id for later resume without
// relying on harness-side hooks, which operator-shared settings may not
// configure (tmux runtime).
func latestClaudeSessionID(home, workDir string) string {
	dir := filepath.Join(home, ".claude", "projects", escapeClaudeProjectDir(workDir))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	var latestID string
	var latestMod time.Time
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().After(latestMod) {
			latestMod = info.ModTime()
			latestID = strings.TrimSuffix(e.Name(), ".jsonl")
		}
	}
	return latestID
}
