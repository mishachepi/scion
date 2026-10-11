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

import "strings"

// GetResumeCommand implements api.SessionResumer from the declarative
// command config's resume_id_flag (e.g. "--resume {session_id}"). The flag
// is split on whitespace and "{session_id}" is substituted per token. It
// returns ok=false when no resume_id_flag is configured or the id is empty,
// so the caller falls back to GetCommand's resume_flag.
func (d *DeclarativeGenericHarness) GetResumeCommand(task, sessionID string, baseArgs []string) ([]string, bool) {
	cmd := d.entry.Command
	if cmd == nil || cmd.ResumeIDFlag == "" || sessionID == "" {
		return nil, false
	}
	args := append([]string{}, cmd.Base...)
	for _, p := range strings.Fields(cmd.ResumeIDFlag) {
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
