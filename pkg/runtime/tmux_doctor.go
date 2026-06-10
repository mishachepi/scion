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
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// tmux 3.0 introduced `new-window -e KEY=VAL`, which the runtime relies on.
const (
	minTmuxMajor = 3
	minTmuxMinor = 0
)

// RunDiagnostics verifies that the host has a tmux binary recent enough to
// drive the runtime and that the tmux server responds.
func (r *TmuxRuntime) RunDiagnostics(_ DiagnosticOpts) DiagnosticReport {
	report := DiagnosticReport{Runtime: r.Name()}

	binCheck := r.checkTmuxBinary()
	report.Checks = append(report.Checks, binCheck)
	if binCheck.Status == "fail" {
		return report
	}

	report.Checks = append(report.Checks, r.checkTmuxVersion())
	report.Checks = append(report.Checks, r.checkTmuxServer())
	return report
}

func (r *TmuxRuntime) checkTmuxBinary() CheckResult {
	if _, err := exec.LookPath(r.Command); err != nil {
		return CheckResult{
			Name:        "tmux-binary",
			Status:      "fail",
			Message:     fmt.Sprintf("tmux binary %q not found in PATH", r.Command),
			Remediation: "Install tmux (brew install tmux on macOS, apt-get install tmux on Debian/Ubuntu).",
		}
	}
	return CheckResult{
		Name:    "tmux-binary",
		Status:  "pass",
		Message: fmt.Sprintf("%s found in PATH", r.Command),
	}
}

func (r *TmuxRuntime) checkTmuxVersion() CheckResult {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	out, err := exec.CommandContext(ctx, r.Command, "-V").Output()
	if err != nil {
		return CheckResult{
			Name:        "tmux-version",
			Status:      "warn",
			Message:     fmt.Sprintf("could not run %s -V: %v", r.Command, err),
			Remediation: "Verify the tmux installation is intact.",
		}
	}
	raw := strings.TrimSpace(string(out))
	major, minor, ok := parseTmuxVersion(raw)
	if !ok {
		return CheckResult{
			Name:        "tmux-version",
			Status:      "warn",
			Message:     fmt.Sprintf("could not parse tmux version from %q", raw),
			Remediation: "If your tmux is older than 3.0, upgrade — the tmux runtime uses `new-window -e KEY=VAL`, added in 3.0.",
		}
	}
	if major < minTmuxMajor || (major == minTmuxMajor && minor < minTmuxMinor) {
		return CheckResult{
			Name:   "tmux-version",
			Status: "fail",
			Message: fmt.Sprintf("tmux %d.%d is too old; runtime requires >= %d.%d for `new-window -e`",
				major, minor, minTmuxMajor, minTmuxMinor),
			Remediation: "Upgrade tmux (brew upgrade tmux on macOS, or build from source).",
		}
	}
	return CheckResult{
		Name:    "tmux-version",
		Status:  "pass",
		Message: raw,
	}
}

func (r *TmuxRuntime) checkTmuxServer() CheckResult {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// `tmux start-server` is a no-op when the server already exists.
	cmd := exec.CommandContext(ctx, r.Command, "start-server")
	if out, err := cmd.CombinedOutput(); err != nil {
		return CheckResult{
			Name:        "tmux-server",
			Status:      "fail",
			Message:     fmt.Sprintf("tmux start-server failed: %v (output: %s)", err, strings.TrimSpace(string(out))),
			Remediation: "Run `tmux start-server` manually to surface the underlying error; common causes are a stale socket in /tmp or insufficient permissions on TMUX_TMPDIR.",
		}
	}
	return CheckResult{
		Name:    "tmux-server",
		Status:  "pass",
		Message: "tmux server reachable",
	}
}

// tmuxVersionPattern tolerates point-release suffixes ("3.4a") and
// pre-release prefixes ("next-3.5").
var tmuxVersionPattern = regexp.MustCompile(`(\d+)\.(\d+)`)

func parseTmuxVersion(versionLine string) (major, minor int, ok bool) {
	match := tmuxVersionPattern.FindStringSubmatch(versionLine)
	if len(match) != 3 {
		return 0, 0, false
	}
	maj, err := strconv.Atoi(match[1])
	if err != nil {
		return 0, 0, false
	}
	min, err := strconv.Atoi(match[2])
	if err != nil {
		return 0, 0, false
	}
	return maj, min, true
}

var _ Diagnosable = (*TmuxRuntime)(nil)
