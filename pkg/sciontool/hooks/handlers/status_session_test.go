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
	"testing"

	"github.com/GoogleCloudPlatform/scion/pkg/sciontool/hooks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStatusHandler_CapturesSessionIDFromSessionStart(t *testing.T) {
	// EvalSymlinks: agent-info.json writes refuse symlinked path components
	// (macOS /var -> /private/var).
	tmpDir, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	statusPath := filepath.Join(tmpDir, "agent-info.json")
	h := &StatusHandler{StatusPath: statusPath}

	// Empty session id → no field written, no error.
	err = h.Handle(&hooks.Event{
		Name: hooks.EventSessionStart, Dialect: "claude",
		Data: hooks.EventData{SessionID: ""},
	})
	require.NoError(t, err)
	info := readAgentInfoMap(t, statusPath)
	_, has := info["harnessSessionId"]
	assert.False(t, has, "empty session id must not write the field")

	// SessionStart with id → field captured.
	err = h.Handle(&hooks.Event{
		Name: hooks.EventSessionStart, Dialect: "claude",
		Data: hooks.EventData{SessionID: "uuid-first"},
	})
	require.NoError(t, err)
	info = readAgentInfoMap(t, statusPath)
	assert.Equal(t, "uuid-first", info["harnessSessionId"])

	// New SessionStart (e.g. after claude --continue spawned a fresh
	// jsonl) → id is replaced, not appended. Pin tracks the head of the
	// session chain so the next `scion resume` aims at the latest jsonl.
	err = h.Handle(&hooks.Event{
		Name: hooks.EventSessionStart, Dialect: "claude",
		Data: hooks.EventData{SessionID: "uuid-second"},
	})
	require.NoError(t, err)
	info = readAgentInfoMap(t, statusPath)
	assert.Equal(t, "uuid-second", info["harnessSessionId"])

	// Non-SessionStart events with a SessionID are NOT captured —
	// the field tracks session boundaries, not every event.
	err = h.Handle(&hooks.Event{
		Name: hooks.EventToolStart, Dialect: "claude",
		Data: hooks.EventData{SessionID: "uuid-leaked", ToolName: "Bash"},
	})
	require.NoError(t, err)
	info = readAgentInfoMap(t, statusPath)
	assert.Equal(t, "uuid-second", info["harnessSessionId"],
		"tool-start session id must not overwrite the pinned id")
}

func TestStatusHandler_UpdateHarnessSessionID_DirectCall(t *testing.T) {
	// EvalSymlinks: agent-info.json writes refuse symlinked path components
	// (macOS /var -> /private/var).
	tmpDir, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	statusPath := filepath.Join(tmpDir, "agent-info.json")
	h := &StatusHandler{StatusPath: statusPath}

	require.NoError(t, h.UpdateHarnessSessionID(""), "empty id is a no-op")
	if _, err := os.Stat(statusPath); err == nil {
		t.Error("empty id must not create agent-info.json")
	}

	require.NoError(t, h.UpdateHarnessSessionID("uuid-x"))
	info := readAgentInfoMap(t, statusPath)
	assert.Equal(t, "uuid-x", info["harnessSessionId"])

	// Idempotent: same id twice is one write (no-op short-circuit).
	require.NoError(t, h.UpdateHarnessSessionID("uuid-x"))
	info = readAgentInfoMap(t, statusPath)
	assert.Equal(t, "uuid-x", info["harnessSessionId"])
}
