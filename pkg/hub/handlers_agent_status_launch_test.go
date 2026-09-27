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

package hub

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/GoogleCloudPlatform/scion/pkg/agent/state"
	"github.com/GoogleCloudPlatform/scion/pkg/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Launch times of two consecutive generations of the same agent. sciontool
// init registers its launch with the first "running" report (startedAt) and
// tags every later report with it (launchStartedAt).
const (
	launchGen1 = "2026-09-27T12:00:00Z"
	launchGen2 = "2026-09-27T12:05:00Z"
)

// postAgentStatus sends a status report the way sciontool does: with the
// agent's own token. The body is a plain map so these tests describe the
// wire contract, not a Go struct.
func postAgentStatus(t *testing.T, srv *Server, agent *store.Agent, body map[string]interface{}) *httptest.ResponseRecorder {
	t.Helper()
	tokenSvc := srv.GetAgentTokenService()
	require.NotNil(t, tokenSvc)
	// A fresh token per report: after a restart the previous generation's
	// credentials are revoked, and a still-running old sciontool adopts the
	// NEW generation's token from the shared token file. Token identity
	// therefore cannot tell the generations apart — only the launch can.
	token, err := tokenSvc.GenerateAgentToken(agent.ID, agent.ProjectID, []AgentTokenScope{ScopeAgentStatusUpdate}, nil)
	require.NoError(t, err)

	raw, err := json.Marshal(body)
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents/"+agent.ID+"/status", bytes.NewReader(raw))
	req.Header.Set("X-Scion-Agent-Token", token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func agentPhase(t *testing.T, s store.Store, id string) string {
	t.Helper()
	got, err := s.GetAgent(context.Background(), id)
	require.NoError(t, err)
	return got.Phase
}

// registerLaunch is sciontool init's first report of a generation.
func registerLaunch(t *testing.T, srv *Server, agent *store.Agent, launch string) {
	t.Helper()
	rec := postAgentStatus(t, srv, agent, map[string]interface{}{
		"phase":     string(state.PhaseRunning),
		"activity":  string(state.ActivityWorking),
		"message":   "Agent started",
		"startedAt": launch,
	})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
}

// TestAgentStatus_SuspendResume_OldGenerationStoppedDoesNotClobberRunning
// reproduces the generation race: after suspend → resume, the previous
// generation's sciontool init exits late and reports its final "stopped".
// That report comes from a launch the hub has already moved past and must
// be rejected, leaving the resumed agent running.
func TestAgentStatus_SuspendResume_OldGenerationStoppedDoesNotClobberRunning(t *testing.T) {
	srv, s := testServer(t)
	disp := &lifecycleResumeDispatcher{}
	srv.SetDispatcher(disp)
	agent := setupBrokerAgentInPhase(t, s, "stale-susp", state.PhaseRunning)

	registerLaunch(t, srv, agent, launchGen1)

	rec := doRequest(t, srv, http.MethodPost, "/api/v1/agents/"+agent.ID+"/suspend", nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, string(state.PhaseSuspended), agentPhase(t, s, agent.ID))

	rec = doRequest(t, srv, http.MethodPost, "/api/v1/agents/"+agent.ID+"/start", nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.True(t, disp.lastStartResume, "start from suspended must resume")

	registerLaunch(t, srv, agent, launchGen2)

	// The old init's final report, sent with the adopted new token.
	rec = postAgentStatus(t, srv, agent, map[string]interface{}{
		"phase":           string(state.PhaseStopped),
		"message":         "Agent stopped",
		"launchStartedAt": launchGen1,
	})
	assert.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
	assert.Equal(t, string(state.PhaseRunning), agentPhase(t, s, agent.ID),
		"a previous generation's final stopped must not overwrite the resumed agent's running")

	// The current generation's own stop is still honored.
	rec = postAgentStatus(t, srv, agent, map[string]interface{}{
		"phase":           string(state.PhaseStopped),
		"message":         "Agent stopped",
		"launchStartedAt": launchGen2,
	})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, string(state.PhaseStopped), agentPhase(t, s, agent.ID))
}

// TestAgentStatus_StopStart_OldGenerationCrashDoesNotClobberRunning covers the
// same race on a plain stop → start. The phase-regression guard deliberately
// lets any phase move to stopped/error, so nothing but the launch check
// protects the new generation here.
func TestAgentStatus_StopStart_OldGenerationCrashDoesNotClobberRunning(t *testing.T) {
	srv, s := testServer(t)
	disp := &lifecycleResumeDispatcher{}
	srv.SetDispatcher(disp)
	agent := setupBrokerAgentInPhase(t, s, "stale-stop", state.PhaseRunning)

	registerLaunch(t, srv, agent, launchGen1)

	rec := doRequest(t, srv, http.MethodPost, "/api/v1/agents/"+agent.ID+"/stop", nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	rec = doRequest(t, srv, http.MethodPost, "/api/v1/agents/"+agent.ID+"/start", nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	registerLaunch(t, srv, agent, launchGen2)

	rec = postAgentStatus(t, srv, agent, map[string]interface{}{
		"phase":           string(state.PhaseStopped),
		"activity":        string(state.ActivityCrashed),
		"launchStartedAt": launchGen1,
	})
	assert.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
	assert.Equal(t, string(state.PhaseRunning), agentPhase(t, s, agent.ID),
		"a previous generation's late crash report must not overwrite the restarted agent's running")
}

// TestAgentStatus_ReportWithoutLaunchIsAccepted keeps older sciontool binaries
// (which do not tag reports with their launch) working unchanged.
func TestAgentStatus_ReportWithoutLaunchIsAccepted(t *testing.T) {
	srv, s := testServer(t)
	agent := setupBrokerAgentInPhase(t, s, "no-launch", state.PhaseRunning)

	registerLaunch(t, srv, agent, launchGen1)

	rec := postAgentStatus(t, srv, agent, map[string]interface{}{
		"phase":   string(state.PhaseStopped),
		"message": "Agent stopped",
	})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, string(state.PhaseStopped), agentPhase(t, s, agent.ID))
}

// TestAgentStatus_NewerLaunchBeforeRegistrationIsAccepted: a restarted
// agent's hooks can report before its init registers the new launch. Their
// tag is newer than the registered launch, and that is not stale.
func TestAgentStatus_NewerLaunchBeforeRegistrationIsAccepted(t *testing.T) {
	srv, s := testServer(t)
	agent := setupBrokerAgentInPhase(t, s, "newer-launch", state.PhaseRunning)

	registerLaunch(t, srv, agent, launchGen1)

	rec := postAgentStatus(t, srv, agent, map[string]interface{}{
		"activity":        string(state.ActivityThinking),
		"launchStartedAt": launchGen2,
	})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	got, err := s.GetAgent(context.Background(), agent.ID)
	require.NoError(t, err)
	assert.Equal(t, string(state.ActivityThinking), got.Activity)
}

// TestAgentStatus_SameLaunchIsAccepted: reports from the registered launch
// pass, including when the tag uses a different but equivalent encoding.
func TestAgentStatus_SameLaunchIsAccepted(t *testing.T) {
	srv, s := testServer(t)
	agent := setupBrokerAgentInPhase(t, s, "same-launch", state.PhaseRunning)

	registerLaunch(t, srv, agent, launchGen1)

	rec := postAgentStatus(t, srv, agent, map[string]interface{}{
		"phase":           string(state.PhaseStopped),
		"launchStartedAt": "2026-09-27T14:00:00+02:00", // == launchGen1
	})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, string(state.PhaseStopped), agentPhase(t, s, agent.ID))
}
