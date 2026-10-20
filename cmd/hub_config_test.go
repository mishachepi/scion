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
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/GoogleCloudPlatform/scion/pkg/config"
	"github.com/GoogleCloudPlatform/scion/pkg/hubsync"
)

const testHubConfigToken = "scion_pat_test-hub-config-token"

// hubConfigTestEnv isolates the globals and environment applyHubConfig
// touches, and simulates a tmux agent of another hub: agent identity in the
// environment and an agent token in ~/.scion/scion-token.
func hubConfigTestEnv(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(envHubConfig, "")
	t.Setenv("SCION_DEV_TOKEN", "")
	t.Setenv("SCION_DEV_TOKEN_FILE", "")
	t.Setenv("SCION_HUB_TOKEN", "")
	t.Setenv("SCION_AUTH_TOKEN", "eyJhbGciOiJIUzI1NiJ9.local-auth-env")
	t.Setenv("SCION_AGENT_SLUG", "orchestrator")
	t.Setenv("SCION_AGENT_NAME", "mch--orchestrator")
	t.Setenv("SCION_AGENT_ID", "11111111-1111-1111-1111-111111111111")
	t.Setenv("SCION_PROJECT", "mch")
	t.Setenv("SCION_PROJECT_ID", "22222222-2222-2222-2222-222222222222")
	t.Setenv("SCION_HUB_ENDPOINT", "http://127.0.0.1:9820")
	t.Setenv("SCION_HUB_URL", "")
	t.Setenv("SCION_HUB_PROJECT_ID", "44444444-4444-4444-4444-444444444444")
	t.Setenv("SCION_HUB_API_KEY", "local-hub-api-key")

	scionDir := filepath.Join(home, ".scion")
	require.NoError(t, os.MkdirAll(scionDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(scionDir, "scion-token"), []byte("eyJhbGciOiJIUzI1NiJ9.local-agent-jwt"), 0o600))

	origHub, origProject, origGlobal, origFlag, origProfile := hubEndpoint, projectPath, globalMode, hubConfigFlag, hubProfileFlag
	origApplied, origFromConfig := hubConfigApplied, projectFromHubConfig
	hubEndpoint, projectPath, globalMode, hubConfigFlag, hubProfileFlag = "", "", false, "", ""
	t.Cleanup(func() {
		hubEndpoint, projectPath, globalMode, hubConfigFlag, hubProfileFlag = origHub, origProject, origGlobal, origFlag, origProfile
		hubConfigApplied, projectFromHubConfig = origApplied, origFromConfig
		hubsync.SetExplicitHubToken("", "")
	})
	return home
}

func writeHubConfig(t *testing.T, dir, endpoint, projectID, tokenFile string) string {
	t.Helper()
	body := "hub:\n  endpoint: " + endpoint + "\n"
	if projectID != "" {
		body += "  project_id: " + projectID + "\n"
	}
	if tokenFile != "" {
		body += "auth:\n  token_file: " + tokenFile + "\n"
	}
	p := filepath.Join(dir, "hub-vm3.yaml")
	require.NoError(t, os.WriteFile(p, []byte(body), 0o644))
	return p
}

func writeHubConfigToken(t *testing.T, dir string, mode os.FileMode) string {
	t.Helper()
	p := filepath.Join(dir, "vm3.pat")
	require.NoError(t, os.WriteFile(p, []byte(testHubConfigToken+"\n"), mode))
	require.NoError(t, os.Chmod(p, mode))
	return p
}

type seenHeaders struct{ auth, agentToken string }

func headerServer(t *testing.T, seen *seenHeaders) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen.auth = r.Header.Get("Authorization")
		seen.agentToken = r.Header.Get("X-Scion-Agent-Token")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	t.Cleanup(s.Close)
	return s
}

// The token from the hub config goes out as the bearer, and neither the
// local agent token file nor SCION_AUTH_TOKEN reaches the configured hub.
func TestApplyHubConfig_TokenOutranksLocalAgentToken_Wire(t *testing.T) {
	dir := hubConfigTestEnv(t)
	var seen seenHeaders
	srv := headerServer(t, &seen)
	hubConfigFlag = writeHubConfig(t, dir, srv.URL, "33333333-3333-3333-3333-333333333333", writeHubConfigToken(t, dir, 0o600))

	require.NoError(t, applyHubConfig())

	client, err := getHubClient(nil)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = client.Health(ctx)

	assert.Equal(t, "Bearer "+testHubConfigToken, seen.auth)
	assert.Empty(t, seen.agentToken, "the local agent token must not be sent to the configured hub")
	assert.Equal(t, "--hub-config token_file", getAuthInfo(nil, srv.URL).Source)
}

// The hub config selects the hub and the project and drops the local agent
// identity from the environment.
func TestApplyHubConfig_SelectsHubProjectAndDropsAgentIdentity(t *testing.T) {
	dir := hubConfigTestEnv(t)
	t.Setenv(envHubConfig, writeHubConfig(t, dir, "https://scion.example.com/", "33333333-3333-3333-3333-333333333333", writeHubConfigToken(t, dir, 0o600)))

	require.NoError(t, applyHubConfig())

	assert.Equal(t, "https://scion.example.com/", hubEndpoint)
	assert.Equal(t, "33333333-3333-3333-3333-333333333333", projectPath)
	for _, name := range agentIdentityEnv {
		_, set := os.LookupEnv(name)
		assert.False(t, set, "%s must be dropped", name)
	}
	assert.False(t, isInAgentContext())
	assert.Equal(t, testHubConfigToken, hubsync.ExplicitHubToken("https://scion.example.com"))
	assert.Empty(t, hubsync.ExplicitHubToken("http://127.0.0.1:9820"), "the token must not be bound to another hub")
}

// 'scion use' and 'scion hub status' name the hub config as the source of
// the hub and of the project it selected.
func TestApplyHubConfig_SourceIsHubConfig(t *testing.T) {
	dir := hubConfigTestEnv(t)
	path := writeHubConfig(t, dir, "https://scion.example.com", "33333333-3333-3333-3333-333333333333", writeHubConfigToken(t, dir, 0o600))
	hubConfigFlag = path

	require.NoError(t, applyHubConfig())

	h := resolveCLIHub()
	assert.Equal(t, cliHub{Endpoint: "https://scion.example.com", Source: useSourceHubConfig, Label: "hub config " + path}, h)
	ut := describeUseTarget(&config.CLIContext{})
	assert.Equal(t, useSourceHubConfig, ut.ProjectSource)
	assert.Equal(t, "hub config "+path, ut.ProjectLabel)
	assert.Equal(t, "33333333-3333-3333-3333-333333333333", ut.ProjectID)
}

// An explicit --project wins over hub.project_id, and the project keeps the
// --project flag as its source.
func TestApplyHubConfig_ExplicitProjectWins(t *testing.T) {
	dir := hubConfigTestEnv(t)
	hubConfigFlag = writeHubConfig(t, dir, "https://scion.example.com", "33333333-3333-3333-3333-333333333333", writeHubConfigToken(t, dir, 0o600))
	projectPath = "other"

	require.NoError(t, applyHubConfig())
	assert.Equal(t, "other", projectPath)
	assert.False(t, projectFromHubConfig)
	assert.Equal(t, useSourceFlag, describeUseTarget(&config.CLIContext{}).ProjectSource)
}

// Without a token, an agent cannot point the CLI at a hub other than its
// own: its credentials would go there.
func TestApplyHubConfig_AgentWithoutTokenForeignHubRefused(t *testing.T) {
	dir := hubConfigTestEnv(t)
	hubConfigFlag = writeHubConfig(t, dir, "https://scion.example.com", "", "")

	err := applyHubConfig()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "auth.token_file is required")
	assert.Empty(t, hubEndpoint)
	assert.Equal(t, "orchestrator", os.Getenv("SCION_AGENT_SLUG"), "identity stays when nothing is applied")
}

func TestApplyHubConfig_Refusals(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, dir string)
		want  string
	}{
		{"relative token file", func(t *testing.T, dir string) {
			hubConfigFlag = writeHubConfig(t, dir, "https://scion.example.com", "", "tokens/vm3.pat")
		}, "absolute path"},
		{"token readable by others", func(t *testing.T, dir string) {
			hubConfigFlag = writeHubConfig(t, dir, "https://scion.example.com", "", writeHubConfigToken(t, dir, 0o644))
		}, "chmod 600"},
		{"missing endpoint", func(t *testing.T, dir string) {
			p := filepath.Join(dir, "c.yaml")
			require.NoError(t, os.WriteFile(p, []byte("hub:\n  project_id: x\n"), 0o644))
			hubConfigFlag = p
		}, "hub.endpoint is required"},
		{"unknown key", func(t *testing.T, dir string) {
			p := filepath.Join(dir, "c.yaml")
			require.NoError(t, os.WriteFile(p, []byte("hub:\n  endpoint: https://scion.example.com\n  token: secret\n"), 0o644))
			hubConfigFlag = p
		}, "token"},
		{"--hub names another hub", func(t *testing.T, dir string) {
			hubConfigFlag = writeHubConfig(t, dir, "https://scion.example.com", "", writeHubConfigToken(t, dir, 0o600))
			hubEndpoint = "https://other.example.com"
		}, "does not match"},
		{"--hub-profile", func(t *testing.T, dir string) {
			hubConfigFlag = writeHubConfig(t, dir, "https://scion.example.com", "", writeHubConfigToken(t, dir, 0o600))
			hubProfileFlag = "target"
		}, "--hub-profile"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := hubConfigTestEnv(t)
			tc.setup(t, dir)
			err := applyHubConfig()
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
			assert.Empty(t, hubsync.ExplicitHubToken("https://scion.example.com"))
		})
	}
}
