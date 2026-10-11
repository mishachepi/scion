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

package harness

import (
	"strings"

	"github.com/GoogleCloudPlatform/scion/pkg/config"
)

// builtinResumeIDFlags holds the resume-by-id flag for harnesses whose
// bundled config.yaml cannot carry resume_id_flag: the bundle must stay valid
// for binaries built before that key existed (see
// TestHarnessConfigs_ValidateAgainstPre3698Schema).
var builtinResumeIDFlags = map[string]string{
	"claude": "--resume {session_id}",
}

// resumeIDFlag returns the command config's resume_id_flag, or the built-in
// flag for the harness when the config sets none.
func (d *DeclarativeGenericHarness) resumeIDFlag() string {
	return resumeIDFlagFor(d.entry)
}

// resumeIDFlagFor is the entry's resume_id_flag, or the built-in flag for
// its harness when the config does not declare one.
func resumeIDFlagFor(entry config.HarnessConfigEntry) string {
	if entry.Command != nil && entry.Command.ResumeIDFlag != "" {
		return entry.Command.ResumeIDFlag
	}
	return builtinResumeIDFlags[entry.Harness]
}

// GetResumeCommand implements api.SessionResumer from the resume-by-id flag
// (resumeIDFlag, e.g. "--resume {session_id}"). The flag is split on
// whitespace and "{session_id}" is substituted per token. It returns
// ok=false when there is no such flag or the id is empty, so the caller
// falls back to GetCommand's resume_flag.
func (d *DeclarativeGenericHarness) GetResumeCommand(task, sessionID string, baseArgs []string) ([]string, bool) {
	cmd := d.entry.Command
	flag := d.resumeIDFlag()
	if cmd == nil || flag == "" || sessionID == "" {
		return nil, false
	}
	args := append([]string{}, cmd.Base...)
	for _, p := range strings.Fields(flag) {
		args = append(args, strings.ReplaceAll(p, "{session_id}", sessionID))
	}
	args = append(args, baseArgs...)
	if task != "" {
		if cmd.TaskFlag != "" {
			args = append(args, cmd.TaskFlag, task)
		} else {
			args = append(args, task)
		}
	}
	return args, true
}
