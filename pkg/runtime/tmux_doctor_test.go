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
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestParseTmuxVersion(t *testing.T) {
	cases := []struct {
		in         string
		wantMajor  int
		wantMinor  int
		wantParsed bool
	}{
		{"tmux 3.4", 3, 4, true},
		{"tmux 3.4a", 3, 4, true},
		{"tmux next-3.5", 3, 5, true},
		{"tmux 3.10", 3, 10, true},
		{"tmux master", 0, 0, false},
		{"", 0, 0, false},
	}
	for _, tc := range cases {
		maj, min, ok := parseTmuxVersion(tc.in)
		if ok != tc.wantParsed {
			t.Errorf("parseTmuxVersion(%q) parsed=%v want %v", tc.in, ok, tc.wantParsed)
			continue
		}
		if ok && (maj != tc.wantMajor || min != tc.wantMinor) {
			t.Errorf("parseTmuxVersion(%q) = (%d,%d); want (%d,%d)",
				tc.in, maj, min, tc.wantMajor, tc.wantMinor)
		}
	}
}

func fakeTmuxDoctor(t *testing.T, tmpDir, versionOutput string, startServerExitCode int) string {
	t.Helper()
	script := `#!/bin/sh
case "$1" in
  -V) printf '%s\n' '` + versionOutput + `'; exit 0 ;;
  start-server) exit ` + strconv.Itoa(startServerExitCode) + ` ;;
  *) exit 0 ;;
esac
`
	path := filepath.Join(tmpDir, "fake-tmux-doctor")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake-tmux-doctor: %v", err)
	}
	return path
}

func TestTmuxRuntime_RunDiagnostics_BinaryMissingShortCircuits(t *testing.T) {
	r := &TmuxRuntime{Command: "/nonexistent/bin/definitely-not-tmux", Session: "scion"}
	report := r.RunDiagnostics(DiagnosticOpts{})

	if len(report.Checks) != 1 || report.Checks[0].Status != "fail" {
		t.Fatalf("expected 1 fail check (short-circuit); got %+v", report.Checks)
	}
	if !strings.Contains(report.Checks[0].Message, "not found in PATH") {
		t.Errorf("message = %q, want 'not found in PATH'", report.Checks[0].Message)
	}
}

func TestTmuxRuntime_RunDiagnostics_HappyPath(t *testing.T) {
	tmpDir := t.TempDir()
	r := &TmuxRuntime{Command: fakeTmuxDoctor(t, tmpDir, "tmux 3.4a", 0), Session: "scion"}
	report := r.RunDiagnostics(DiagnosticOpts{})

	if len(report.Checks) != 3 {
		t.Fatalf("expected 3 checks; got %d: %+v", len(report.Checks), report.Checks)
	}
	for _, c := range report.Checks {
		if c.Status != "pass" {
			t.Errorf("check %s = %s (%s); want pass", c.Name, c.Status, c.Message)
		}
	}
}

func TestTmuxRuntime_RunDiagnostics_VersionTooOldFails(t *testing.T) {
	tmpDir := t.TempDir()
	r := &TmuxRuntime{Command: fakeTmuxDoctor(t, tmpDir, "tmux 2.9", 0), Session: "scion"}
	report := r.RunDiagnostics(DiagnosticOpts{})

	for _, c := range report.Checks {
		if c.Name == "tmux-version" {
			if c.Status != "fail" {
				t.Errorf("version check on tmux 2.9 = %q, want fail", c.Status)
			}
			if !strings.Contains(c.Message, "too old") {
				t.Errorf("version message = %q, want 'too old'", c.Message)
			}
			return
		}
	}
	t.Errorf("tmux-version check missing from report: %+v", report.Checks)
}

func TestTmuxRuntime_RunDiagnostics_ServerStartFailureSurfaces(t *testing.T) {
	tmpDir := t.TempDir()
	r := &TmuxRuntime{Command: fakeTmuxDoctor(t, tmpDir, "tmux 3.4", 1), Session: "scion"}
	report := r.RunDiagnostics(DiagnosticOpts{})

	for _, c := range report.Checks {
		if c.Name == "tmux-server" {
			if c.Status != "fail" {
				t.Errorf("server check = %q, want fail when start-server exits non-zero", c.Status)
			}
			return
		}
	}
	t.Errorf("tmux-server check missing from report: %+v", report.Checks)
}
