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

package handlers

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/GoogleCloudPlatform/scion/pkg/api"
	"github.com/GoogleCloudPlatform/scion/pkg/sciontool/hooks"
)

// captureHarnessSessionID records the harness's native session id from a
// SessionStart event. Claude Code starts a new session id on every
// --continue/--resume and on /clear, so the most recent one is kept: a later
// resume aims at the head of the chain.
func (h *StatusHandler) captureHarnessSessionID(event *hooks.Event) error {
	if event.Name != hooks.EventSessionStart || event.Data.SessionID == "" {
		return nil
	}
	return h.UpdateHarnessSessionID(event.Data.SessionID)
}

// harnessSessionIDPath is api.HarnessSessionIDFile next to agent-info.json.
func (h *StatusHandler) harnessSessionIDPath() string {
	return filepath.Join(filepath.Dir(h.StatusPath), api.HarnessSessionIDFile)
}

// UpdateHarnessSessionID saves the harness's native session id to
// api.HarnessSessionIDFile. agent-info.json is not touched. No-op for an
// empty or unchanged id.
func (h *StatusHandler) UpdateHarnessSessionID(sessionID string) error {
	if sessionID == "" {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	path := h.harnessSessionIDPath()
	if data, err := os.ReadFile(path); err == nil && strings.TrimSpace(string(data)) == sessionID {
		return nil
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(sessionID+"\n"), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
