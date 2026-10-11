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

package api

// SessionResumer is an optional Harness capability: build the command that
// resumes one exact harness session by its id, instead of the harness's
// latest-session flag (e.g. "claude --continue", which picks the most recent
// session per working directory and so cross-talks when several agents
// share one directory). ok=false means the harness has no id-aware resume
// flag configured; callers then fall back to GetCommand(task, true, baseArgs).
type SessionResumer interface {
	GetResumeCommand(task, sessionID string, baseArgs []string) (args []string, ok bool)
}

// HarnessCommand returns the harness command for a start: the exact-session
// resume command when resume is set, sessionID is known and h implements
// SessionResumer with an id-aware flag; h.GetCommand otherwise.
func HarnessCommand(h Harness, task string, resume bool, sessionID string, baseArgs []string) []string {
	if resume && sessionID != "" {
		if r, ok := h.(SessionResumer); ok {
			if args, ok := r.GetResumeCommand(task, sessionID, baseArgs); ok {
				return args
			}
		}
	}
	return h.GetCommand(task, resume, baseArgs)
}
