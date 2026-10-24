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

package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/GoogleCloudPlatform/scion/pkg/hubclient"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	localProjectID = "11111111-1111-4111-8111-111111111111"
	otherProjectID = "22222222-2222-4222-8222-222222222222"
)

// foreignProjectHub serves two projects, "local-proj" (the project of the
// current directory) and "other-proj", and records the create requests.
type foreignProjectHub struct {
	srv *httptest.Server

	mu      sync.Mutex
	creates []hubclient.CreateAgentRequest
	deletes []string
}

func newForeignProjectHub(t *testing.T) *foreignProjectHub {
	t.Helper()
	h := &foreignProjectHub{}
	projects := map[string]string{"local-proj": localProjectID, "other-proj": otherProjectID}
	h.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		path := r.URL.Path
		switch {
		case path == "/healthz":
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
		case r.Method == http.MethodGet && path == "/api/v1/projects":
			var list []map[string]string
			if id, ok := projects[r.URL.Query().Get("slug")]; ok {
				list = append(list, map[string]string{"id": id, "slug": r.URL.Query().Get("slug"), "name": r.URL.Query().Get("slug")})
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"projects": list})
		case r.Method == http.MethodPost && strings.HasSuffix(path, "/agents"):
			var req hubclient.CreateAgentRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			h.mu.Lock()
			h.creates = append(h.creates, req)
			h.mu.Unlock()
			_ = json.NewEncoder(w).Encode(&hubclient.CreateAgentResponse{
				Agent: &hubclient.Agent{
					ID: "agent-1", Slug: req.Name, Name: req.Name,
					Status: "running", Phase: "running",
					RuntimeBrokerID: "broker-1", RuntimeBrokerName: "broker",
					Created: time.Now().UTC(),
				},
			})
		case r.Method == http.MethodDelete && strings.Contains(path, "/agents/"):
			h.mu.Lock()
			h.deletes = append(h.deletes, path)
			h.mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(h.srv.Close)
	return h
}

// setupNonGitProject makes a non-git project linked to localProjectID with
// a workspace file and a sync state, and makes it the current directory.
// It returns the state.yaml path.
func setupNonGitProject(t *testing.T, hubURL string) string {
	t.Helper()
	hermeticCLIEnv(t)
	t.Setenv("SCION_DEV_TOKEN", "scion_dev_test")
	t.Setenv("SCION_HUB_TOKEN", "")

	dir := filepath.Join(t.TempDir(), "local-proj")
	scionDir := filepath.Join(dir, ".scion")
	require.NoError(t, os.MkdirAll(scionDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(scionDir, "settings.yaml"),
		[]byte("project_id: "+localProjectID+"\nhub:\n  project_id: "+localProjectID+"\n"), 0o644))
	statePath := filepath.Join(scionDir, "state.yaml")
	require.NoError(t, os.WriteFile(statePath,
		[]byte("last_synced_at: \"2026-01-01T00:00:00Z\"\nsynced_agents:\n  - existing-agent\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("local workspace file"), 0o644))
	t.Chdir(dir)

	origProjectPath, origGlobal, origYes, origNI, origEP := projectPath, globalMode, autoConfirm, nonInteractive, hubEndpoint
	origOutput, origTemplate, origHarness, origBroker := outputFormat, templateName, harnessConfigFlag, runtimeBrokerID
	t.Cleanup(func() {
		projectPath, globalMode, autoConfirm, nonInteractive, hubEndpoint = origProjectPath, origGlobal, origYes, origNI, origEP
		outputFormat, templateName, harnessConfigFlag, runtimeBrokerID = origOutput, origTemplate, origHarness, origBroker
	})
	globalMode, autoConfirm, nonInteractive, hubEndpoint = false, true, true, hubURL
	outputFormat, templateName, harnessConfigFlag, runtimeBrokerID = "json", "", "codex", "broker-1"
	return statePath
}

// TestForeignProjectRef_KeepsLocalProjectUntouched: start, create and delete
// with --project <slug of another project>, run from a non-git project,
// upload none of the local files and leave the local state.yaml as it was.
func TestForeignProjectRef_KeepsLocalProjectUntouched(t *testing.T) {
	hub := newForeignProjectHub(t)
	statePath := setupNonGitProject(t, hub.srv.URL)
	before, err := os.ReadFile(statePath)
	require.NoError(t, err)

	projectPath = "other-proj"
	var hubCtx *HubContext
	captureStdIO(t, func() { hubCtx, err = requireHub(projectPath) })
	require.NoError(t, err)
	require.Equal(t, otherProjectID, hubCtx.ProjectID)
	assert.True(t, hubCtx.ForeignProject)

	captureStdIO(t, func() {
		require.NoError(t, startAgentViaHub(nil, hubCtx, "foreign-start", "hello", false, nil))
		require.NoError(t, createAgentViaHub(hubCtx, "foreign-create", "hello", nil))
		require.NoError(t, deleteAgentsViaHub(hubCtx, []string{"foreign-start"}))
	})

	require.Len(t, hub.creates, 2)
	for _, req := range hub.creates {
		assert.Empty(t, req.WorkspaceFiles, "agent %s: no local files uploaded", req.Name)
	}
	require.Len(t, hub.deletes, 1)
	assert.Contains(t, hub.deletes[0], otherProjectID)

	after, err := os.ReadFile(statePath)
	require.NoError(t, err)
	assert.Equal(t, string(before), string(after), "local state.yaml unchanged")
}

// TestOwnProjectRef_RecordsLocalState: --project <slug of the current
// project> is the local project, so its files are uploaded and its sync
// state records the agent, as without --project.
func TestOwnProjectRef_RecordsLocalState(t *testing.T) {
	hub := newForeignProjectHub(t)
	statePath := setupNonGitProject(t, hub.srv.URL)

	projectPath = "local-proj"
	var hubCtx *HubContext
	var err error
	captureStdIO(t, func() { hubCtx, err = requireHub(projectPath) })
	require.NoError(t, err)
	require.Equal(t, localProjectID, hubCtx.ProjectID)
	assert.False(t, hubCtx.ForeignProject)

	captureStdIO(t, func() {
		require.NoError(t, startAgentViaHub(nil, hubCtx, "own-start", "hello", false, nil))
	})
	require.Len(t, hub.creates, 1)
	assert.NotEmpty(t, hub.creates[0].WorkspaceFiles)

	after, err := os.ReadFile(statePath)
	require.NoError(t, err)
	assert.Contains(t, string(after), "own-start")
}
