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
	"testing"

	"github.com/GoogleCloudPlatform/scion/pkg/api"
	"github.com/GoogleCloudPlatform/scion/pkg/sciontool/hooks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// savedSessionID reads the id UpdateHarnessSessionID saved next to
// statusPath ("" when the file does not exist).
func savedSessionID(t *testing.T, statusPath string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(filepath.Dir(statusPath), api.HarnessSessionIDFile))
	if os.IsNotExist(err) {
		return ""
	}
	require.NoError(t, err)
	return strings.TrimSpace(string(data))
}

func TestStatusHandler_CapturesSessionIDFromSessionStart(t *testing.T) {
	// EvalSymlinks: agent-info.json writes refuse symlinked path components
	// (macOS /var -> /private/var).
	tmpDir, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	statusPath := filepath.Join(tmpDir, "agent-info.json")
	h := &StatusHandler{StatusPath: statusPath}

	// Empty session id: nothing saved, no error.
	err = h.Handle(&hooks.Event{
		Name: hooks.EventSessionStart, Dialect: "claude",
		Data: hooks.EventData{SessionID: ""},
	})
	require.NoError(t, err)
	assert.Equal(t, "", savedSessionID(t, statusPath))

	// SessionStart with an id: saved.
	err = h.Handle(&hooks.Event{
		Name: hooks.EventSessionStart, Dialect: "claude",
		Data: hooks.EventData{SessionID: "uuid-first"},
	})
	require.NoError(t, err)
	assert.Equal(t, "uuid-first", savedSessionID(t, statusPath))
	_, inInfo := readAgentInfoMap(t, statusPath)["harnessSessionId"]
	assert.False(t, inInfo, "the id is not written to agent-info.json")

	// A new SessionStart (claude --continue or /clear starts a fresh jsonl)
	// replaces the id, so the next resume aims at the latest session.
	err = h.Handle(&hooks.Event{
		Name: hooks.EventSessionStart, Dialect: "claude",
		Data: hooks.EventData{SessionID: "uuid-second"},
	})
	require.NoError(t, err)
	assert.Equal(t, "uuid-second", savedSessionID(t, statusPath))

	// Other events carrying a SessionID are not captured.
	err = h.Handle(&hooks.Event{
		Name: hooks.EventToolStart, Dialect: "claude",
		Data: hooks.EventData{SessionID: "uuid-leaked", ToolName: "Bash"},
	})
	require.NoError(t, err)
	assert.Equal(t, "uuid-second", savedSessionID(t, statusPath),
		"tool-start session id must not overwrite the saved id")
}

func TestStatusHandler_UpdateHarnessSessionID_DirectCall(t *testing.T) {
	tmpDir, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	statusPath := filepath.Join(tmpDir, "agent-info.json")
	h := &StatusHandler{StatusPath: statusPath}

	require.NoError(t, h.UpdateHarnessSessionID(""), "empty id is a no-op")
	assert.Equal(t, "", savedSessionID(t, statusPath))

	require.NoError(t, h.UpdateHarnessSessionID("uuid-x"))
	assert.Equal(t, "uuid-x", savedSessionID(t, statusPath))
	require.NoError(t, h.UpdateHarnessSessionID("uuid-x"))
	assert.Equal(t, "uuid-x", savedSessionID(t, statusPath))
	if _, err := os.Stat(statusPath); err == nil {
		t.Error("UpdateHarnessSessionID must not create agent-info.json")
	}
}
