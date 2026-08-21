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

	"github.com/GoogleCloudPlatform/scion/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetAuthInfo_NoAuth(t *testing.T) {
	// Clear all dev token sources so getAuthInfo doesn't find dev auth
	t.Setenv("SCION_DEV_TOKEN", "")
	t.Setenv("SCION_AUTH_TOKEN", "")
	t.Setenv("SCION_DEV_TOKEN_FILE", "")
	t.Setenv("SCION_HUB_TOKEN", "")
	t.Setenv("HOME", t.TempDir())

	settings := &config.Settings{}
	info := getAuthInfo(settings, "https://hub.example.com")
	assert.Equal(t, "none", info.MethodType)
	assert.Equal(t, "none", info.Method)
}

func TestGetAuthInfo_DeprecatedTokenIgnored(t *testing.T) {
	// Clear higher-priority token sources
	t.Setenv("SCION_AUTH_TOKEN", "")
	t.Setenv("SCION_DEV_TOKEN", "")
	t.Setenv("SCION_DEV_TOKEN_FILE", "")
	t.Setenv("SCION_HUB_TOKEN", "")
	t.Setenv("HOME", t.TempDir())

	// hub.token is deprecated and should no longer be used for auth
	settings := &config.Settings{
		Hub: &config.HubClientConfig{
			Token: "test-token",
		},
	}
	info := getAuthInfo(settings, "https://hub.example.com")
	// Should NOT return bearer — token is deprecated
	assert.NotEqual(t, "bearer", info.MethodType)
}

func TestGetAuthInfo_DeprecatedAPIKeyIgnored(t *testing.T) {
	// Clear higher-priority token sources
	t.Setenv("SCION_AUTH_TOKEN", "")
	t.Setenv("SCION_DEV_TOKEN", "")
	t.Setenv("SCION_DEV_TOKEN_FILE", "")
	t.Setenv("SCION_HUB_TOKEN", "")
	t.Setenv("HOME", t.TempDir())

	// hub.apiKey is deprecated and should no longer be used for auth
	settings := &config.Settings{
		Hub: &config.HubClientConfig{
			APIKey: "test-api-key",
		},
	}
	info := getAuthInfo(settings, "https://hub.example.com")
	// Should NOT return apikey — apiKey is deprecated
	assert.NotEqual(t, "apikey", info.MethodType)
}

func TestGetAuthInfo_EnvTokenTakesPriority(t *testing.T) {
	// Clear higher-priority token sources so SCION_HUB_TOKEN is reached
	t.Setenv("SCION_AUTH_TOKEN", "")
	t.Setenv("SCION_DEV_TOKEN", "")
	t.Setenv("SCION_DEV_TOKEN_FILE", "")
	t.Setenv("HOME", t.TempDir())

	// SCION_HUB_TOKEN env var should work for bearer auth
	settings := &config.Settings{}
	t.Setenv("SCION_HUB_TOKEN", "env-token")
	info := getAuthInfo(settings, "https://hub.example.com")
	assert.Equal(t, "bearer", info.MethodType)
	assert.Equal(t, "SCION_HUB_TOKEN env", info.Source)
}

func TestGetAuthInfo_NilHub(t *testing.T) {
	// Clear all dev token sources so getAuthInfo doesn't find dev auth
	t.Setenv("SCION_DEV_TOKEN", "")
	t.Setenv("SCION_AUTH_TOKEN", "")
	t.Setenv("SCION_DEV_TOKEN_FILE", "")
	t.Setenv("SCION_HUB_TOKEN", "")
	t.Setenv("HOME", t.TempDir())

	settings := &config.Settings{
		Hub: nil,
	}
	info := getAuthInfo(settings, "")
	assert.Equal(t, "none", info.MethodType)
}

func TestGetAuthInfo_DevAuthPreferredOverStaleAgentTokenOnLocalhost(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("SCION_AUTH_TOKEN", "")
	t.Setenv("SCION_DEV_TOKEN", "")
	t.Setenv("SCION_DEV_TOKEN_FILE", "")
	t.Setenv("SCION_HUB_TOKEN", "")
	// This scenario models a human CLI invocation outside any agent
	// container — clear the agent-context vars so the test is hermetic
	// even when run from inside a real agent (see isInAgentContext).
	t.Setenv("SCION_AGENT_SLUG", "")
	t.Setenv("SCION_AGENT_NAME", "")

	scionDir := filepath.Join(tmpDir, ".scion")
	if err := os.MkdirAll(scionDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Write a non-dev agent token (stale JWT from a previous remote hub)
	if err := os.WriteFile(filepath.Join(scionDir, "scion-token"), []byte("eyJhbGciOiJIUzI1NiJ9.stale-jwt"), 0600); err != nil {
		t.Fatal(err)
	}

	// Write a dev token (from the currently running local server)
	if err := os.WriteFile(filepath.Join(scionDir, "dev-token"), []byte("scion_dev_abc123"), 0600); err != nil {
		t.Fatal(err)
	}

	settings := &config.Settings{}
	info := getAuthInfo(settings, "http://localhost:8080")
	assert.Equal(t, "devauth", info.MethodType)
	assert.Equal(t, "Dev auth", info.Method)
	assert.True(t, info.IsDevAuth)
}

func TestGetAuthInfo_AgentTokenPreferredInAgentContextOnLocalhost(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("SCION_AUTH_TOKEN", "")
	t.Setenv("SCION_DEV_TOKEN", "")
	t.Setenv("SCION_DEV_TOKEN_FILE", "")
	t.Setenv("SCION_HUB_TOKEN", "")
	// Simulate a hub-provisioned agent container: the on-disk agent token is
	// this process's real identity and must not be shadowed by dev auth,
	// even against a localhost hub endpoint.
	t.Setenv("SCION_AGENT_SLUG", "area-scion")
	t.Setenv("SCION_AGENT_NAME", "mch--area-scion")

	scionDir := filepath.Join(tmpDir, ".scion")
	if err := os.MkdirAll(scionDir, 0755); err != nil {
		t.Fatal(err)
	}

	// A real (non-dev) agent token, as hub provisioning would write for a
	// running container.
	if err := os.WriteFile(filepath.Join(scionDir, "scion-token"), []byte("eyJhbGciOiJIUzI1NiJ9.real-agent-jwt"), 0600); err != nil {
		t.Fatal(err)
	}

	// A dev token also present, e.g. because the local hub runs in dev mode.
	if err := os.WriteFile(filepath.Join(scionDir, "dev-token"), []byte("scion_dev_abc123"), 0600); err != nil {
		t.Fatal(err)
	}

	settings := &config.Settings{}
	info := getAuthInfo(settings, "http://localhost:8080")
	assert.Equal(t, "agent_token", info.MethodType)
	assert.Equal(t, "Agent token", info.Method)
	assert.Equal(t, "scion-token file", info.Source)
	assert.False(t, info.IsDevAuth)
}

// TestGetHubClient_AgentContextSendsAgentTokenHeaderNotDevBearer is the
// wire-level regression test for the dev-token-preemption bug: on a
// localhost hub, inside an agent container, the client built by
// getHubClient must authenticate with the real agent token via
// X-Scion-Agent-Token — not the dev token via a plain Bearer header. The
// hub's agent-identity-scoped routes (e.g. outbound-message) require the
// former and reject the latter with 401 regardless of endpoint.
func TestGetHubClient_AgentContextSendsAgentTokenHeaderNotDevBearer(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("SCION_AUTH_TOKEN", "")
	t.Setenv("SCION_DEV_TOKEN", "")
	t.Setenv("SCION_DEV_TOKEN_FILE", "")
	t.Setenv("SCION_HUB_TOKEN", "")
	t.Setenv("SCION_AGENT_SLUG", "area-scion")
	t.Setenv("SCION_AGENT_NAME", "mch--area-scion")

	scionDir := filepath.Join(tmpDir, ".scion")
	require.NoError(t, os.MkdirAll(scionDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(scionDir, "scion-token"), []byte("eyJhbGciOiJIUzI1NiJ9.real-agent-jwt"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(scionDir, "dev-token"), []byte("scion_dev_abc123"), 0600))

	var gotAgentTokenHeader, gotAuthHeader string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAgentTokenHeader = r.Header.Get("X-Scion-Agent-Token")
		gotAuthHeader = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer server.Close()

	origHubEndpoint := hubEndpoint
	hubEndpoint = ""
	defer func() { hubEndpoint = origHubEndpoint }()

	settings := &config.Settings{Hub: &config.HubClientConfig{Endpoint: server.URL}}
	client, err := getHubClient(settings)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = client.Health(ctx)

	assert.Equal(t, "eyJhbGciOiJIUzI1NiJ9.real-agent-jwt", gotAgentTokenHeader,
		"expected the real agent token on X-Scion-Agent-Token")
	assert.Empty(t, gotAuthHeader, "dev token must not go out as a Bearer Authorization header in agent context")
}

func TestGetAuthInfo_AgentTokenUsedOnRemoteEndpoint(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("SCION_AUTH_TOKEN", "")
	t.Setenv("SCION_DEV_TOKEN", "")
	t.Setenv("SCION_DEV_TOKEN_FILE", "")
	t.Setenv("SCION_HUB_TOKEN", "")

	scionDir := filepath.Join(tmpDir, ".scion")
	if err := os.MkdirAll(scionDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Write a non-dev agent token
	if err := os.WriteFile(filepath.Join(scionDir, "scion-token"), []byte("eyJhbGciOiJIUzI1NiJ9.valid-jwt"), 0600); err != nil {
		t.Fatal(err)
	}

	// Write a dev token (leftover from a previous local server)
	if err := os.WriteFile(filepath.Join(scionDir, "dev-token"), []byte("scion_dev_abc123"), 0600); err != nil {
		t.Fatal(err)
	}

	settings := &config.Settings{}
	info := getAuthInfo(settings, "https://hub.example.com")
	assert.Equal(t, "agent_token", info.MethodType)
	assert.Equal(t, "Agent token", info.Method)
	assert.Equal(t, "scion-token file", info.Source)
}

func TestGetAuthInfo_DevAgentTokenUsedDirectly(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("SCION_AUTH_TOKEN", "")
	t.Setenv("SCION_DEV_TOKEN", "")
	t.Setenv("SCION_DEV_TOKEN_FILE", "")
	t.Setenv("SCION_HUB_TOKEN", "")

	scionDir := filepath.Join(tmpDir, ".scion")
	if err := os.MkdirAll(scionDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Write a dev token in the scion-token file (agent launched by dev server)
	if err := os.WriteFile(filepath.Join(scionDir, "scion-token"), []byte("scion_dev_abc123"), 0600); err != nil {
		t.Fatal(err)
	}

	settings := &config.Settings{}
	info := getAuthInfo(settings, "http://localhost:8080")
	assert.Equal(t, "agent_token", info.MethodType)
	assert.Equal(t, "Agent token (dev)", info.Method)
	assert.True(t, info.IsDevAuth)
}

func TestIsLocalhostEndpoint(t *testing.T) {
	assert.True(t, isLocalhostEndpoint("http://localhost:8080"))
	assert.True(t, isLocalhostEndpoint("https://localhost:443"))
	assert.True(t, isLocalhostEndpoint("http://127.0.0.1:8080"))
	assert.True(t, isLocalhostEndpoint("http://[::1]:8080"))
	assert.False(t, isLocalhostEndpoint("https://hub.example.com"))
	assert.False(t, isLocalhostEndpoint("http://192.168.1.100:8080"))
	assert.False(t, isLocalhostEndpoint(""))
}

func TestGetHubEnabledScope_GlobalScope(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SCION_HUB_ENDPOINT", "")

	enabled := true
	settings := &config.Settings{
		Hub: &config.HubClientConfig{Enabled: &enabled},
	}

	scope := getHubEnabledScope("/some/path", true, settings)
	assert.Equal(t, "global", scope.Scope)
	assert.False(t, scope.Inherited)
	assert.True(t, scope.Enabled)
}

func TestGetHubEnabledScope_ProjectHasOwnSetting(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("SCION_HUB_ENDPOINT", "")

	// Create project settings with hub.enabled
	projectDir := filepath.Join(tmpDir, "project-scion")
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "settings.yaml"),
		[]byte("hub:\n  enabled: true\n"), 0644); err != nil {
		t.Fatal(err)
	}

	enabled := true
	settings := &config.Settings{
		Hub: &config.HubClientConfig{Enabled: &enabled},
	}

	scope := getHubEnabledScope(projectDir, false, settings)
	assert.Equal(t, "project", scope.Scope)
	assert.False(t, scope.Inherited)
	assert.True(t, scope.Enabled)
}

func TestGetHubEnabledScope_InheritedFromGlobal(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("SCION_HUB_ENDPOINT", "")

	// Create global settings with hub.enabled
	globalDir := filepath.Join(tmpDir, ".scion")
	if err := os.MkdirAll(globalDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(globalDir, "settings.yaml"),
		[]byte("hub:\n  enabled: true\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Create project settings WITHOUT hub.enabled
	projectDir := filepath.Join(tmpDir, "project-scion")
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "settings.yaml"),
		[]byte("runtime: docker\n"), 0644); err != nil {
		t.Fatal(err)
	}

	enabled := true
	settings := &config.Settings{
		Hub: &config.HubClientConfig{Enabled: &enabled},
	}

	scope := getHubEnabledScope(projectDir, false, settings)
	assert.Equal(t, "global", scope.Scope)
	assert.True(t, scope.Inherited)
	assert.True(t, scope.Enabled)
}

func TestGetHubEnabledScope_DefaultWhenNothingSet(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("SCION_HUB_ENDPOINT", "")

	// Create empty global dir
	globalDir := filepath.Join(tmpDir, ".scion")
	if err := os.MkdirAll(globalDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create project settings WITHOUT hub.enabled
	projectDir := filepath.Join(tmpDir, "project-scion")
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatal(err)
	}

	settings := &config.Settings{}

	scope := getHubEnabledScope(projectDir, false, settings)
	assert.Equal(t, "default", scope.Scope)
	assert.False(t, scope.Inherited)
	assert.False(t, scope.Enabled)
}

func TestGetHubEndpointScope_FromProject(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("SCION_HUB_ENDPOINT", "")

	// Save original hubEndpoint and restore after test
	origHubEndpoint := hubEndpoint
	hubEndpoint = ""
	defer func() { hubEndpoint = origHubEndpoint }()

	// Create project settings with hub.endpoint
	projectDir := filepath.Join(tmpDir, "project-scion")
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "settings.yaml"),
		[]byte("hub:\n  endpoint: https://project-hub.example.com\n"), 0644); err != nil {
		t.Fatal(err)
	}

	settings := &config.Settings{
		Hub: &config.HubClientConfig{Endpoint: "https://project-hub.example.com"},
	}

	scope := getHubEndpointScope(projectDir, false, settings)
	assert.Equal(t, "project", scope.Source)
	assert.False(t, scope.Inherited)
	assert.Equal(t, "https://project-hub.example.com", scope.Endpoint)
}

func TestGetHubEndpointScope_InheritedFromGlobal(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("SCION_HUB_ENDPOINT", "")

	origHubEndpoint := hubEndpoint
	hubEndpoint = ""
	defer func() { hubEndpoint = origHubEndpoint }()

	// Create global settings with hub.endpoint
	globalDir := filepath.Join(tmpDir, ".scion")
	if err := os.MkdirAll(globalDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(globalDir, "settings.yaml"),
		[]byte("hub:\n  endpoint: https://global-hub.example.com\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Create project settings WITHOUT hub.endpoint
	projectDir := filepath.Join(tmpDir, "project-scion")
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "settings.yaml"),
		[]byte("runtime: docker\n"), 0644); err != nil {
		t.Fatal(err)
	}

	settings := &config.Settings{
		Hub: &config.HubClientConfig{Endpoint: "https://global-hub.example.com"},
	}

	scope := getHubEndpointScope(projectDir, false, settings)
	assert.Equal(t, "global", scope.Source)
	assert.True(t, scope.Inherited)
	assert.Equal(t, "https://global-hub.example.com", scope.Endpoint)
}

func TestGetHubEndpointScope_FromEnv(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("SCION_HUB_ENDPOINT", "https://env-hub.example.com")

	origHubEndpoint := hubEndpoint
	hubEndpoint = ""
	defer func() { hubEndpoint = origHubEndpoint }()

	// Create empty global dir
	globalDir := filepath.Join(tmpDir, ".scion")
	if err := os.MkdirAll(globalDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create project settings WITHOUT hub.endpoint
	projectDir := filepath.Join(tmpDir, "project-scion")
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatal(err)
	}

	settings := &config.Settings{}

	scope := getHubEndpointScope(projectDir, false, settings)
	assert.Equal(t, "env", scope.Source)
	assert.True(t, scope.Inherited)
}

func TestGetHubEndpointScope_FromFlag(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SCION_HUB_ENDPOINT", "")

	origHubEndpoint := hubEndpoint
	hubEndpoint = "https://flag-hub.example.com"
	defer func() { hubEndpoint = origHubEndpoint }()

	settings := &config.Settings{}

	scope := getHubEndpointScope("/some/path", false, settings)
	assert.Equal(t, "flag", scope.Source)
	assert.False(t, scope.Inherited)
	assert.Equal(t, "https://flag-hub.example.com", scope.Endpoint)
}

func TestParseJWTExpiry_ValidToken(t *testing.T) {
	// Build a minimal JWT with exp claim (header.payload.signature)
	// Header: {"alg":"HS256","typ":"JWT"}
	header := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9"
	// Payload: {"exp":1700000000} -> 2023-11-14T22:13:20Z
	payload := "eyJleHAiOjE3MDAwMDAwMDB9"
	token := header + "." + payload + ".fakesig"

	expiry := parseJWTExpiry(token)
	assert.NotNil(t, expiry)
	assert.Equal(t, int64(1700000000), expiry.Unix())
}

func TestParseJWTExpiry_NoExpClaim(t *testing.T) {
	header := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9"
	// Payload: {"sub":"test"}
	payload := "eyJzdWIiOiJ0ZXN0In0"
	token := header + "." + payload + ".fakesig"

	expiry := parseJWTExpiry(token)
	assert.Nil(t, expiry)
}

func TestParseJWTExpiry_InvalidToken(t *testing.T) {
	assert.Nil(t, parseJWTExpiry("not-a-jwt"))
	assert.Nil(t, parseJWTExpiry(""))
	assert.Nil(t, parseJWTExpiry("a.!!!invalid-base64!!!.c"))
}

func TestParseDefaultBranch_ParsesSymref(t *testing.T) {
	// Real output from `git ls-remote --symref <url> HEAD`
	output := "ref: refs/heads/main\tHEAD\n5f3c6e72abc123def456 HEAD\n"
	result := parseDefaultBranch(output)
	assert.Equal(t, "main", result)
}

func TestParseDefaultBranch_NonMainBranch(t *testing.T) {
	output := "ref: refs/heads/develop\tHEAD\nabc123 HEAD\n"
	result := parseDefaultBranch(output)
	assert.Equal(t, "develop", result)
}

func TestParseDefaultBranch_NoMatch(t *testing.T) {
	// Output that doesn't contain the expected symref line
	output := "abc123def456 HEAD\n"
	result := parseDefaultBranch(output)
	assert.Equal(t, "", result)
}

func TestParseDefaultBranch_EmptyOutput(t *testing.T) {
	result := parseDefaultBranch("")
	assert.Equal(t, "", result)
}
