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

// GetResumeCommand implements api.SessionResumer for script harnesses such
// as claude: it builds the same invocation as a resume through GetCommand,
// with the resume flag replaced by the session-pinned resume_id_flag, so the
// task, system-prompt and argument-order rules stay those of GetCommand.
// It reports false when the config has no command block, no resume id flag
// applies, or sessionID is empty; the caller then resumes with resume_flag.
func (c *ContainerScriptHarness) GetResumeCommand(task, sessionID string, baseArgs []string) ([]string, bool) {
	cmd := c.entry.Command
	flag := resumeIDFlagFor(c.entry)
	if cmd == nil || flag == "" || sessionID == "" {
		return nil, false
	}
	pinned := *cmd
	pinned.ResumeFlag = strings.ReplaceAll(flag, "{session_id}", sessionID)
	cc := *c
	cc.entry.Command = &pinned
	return cc.GetCommand(task, true, baseArgs), true
}
