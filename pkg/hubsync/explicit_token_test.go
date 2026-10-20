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

package hubsync

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
)

// The client EnsureHubReady builds (createHubClient) sends the explicit
// token to its endpoint, not the local agent token; any other endpoint
// keeps the usual credential order.
func TestCreateHubClient_ExplicitTokenOutranksAgentToken_Wire(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SCION_AUTH_TOKEN", "")
	t.Setenv("SCION_HUB_TOKEN", "")
	t.Setenv("SCION_DEV_TOKEN", "")
	t.Setenv("SCION_DEV_TOKEN_FILE", "")
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".scion"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(home, ".scion", "scion-token"), []byte("eyJhbGciOiJIUzI1NiJ9.local-agent-jwt"), 0o600))

	var gotAuth, gotAgent string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotAgent = r.Header.Get("X-Scion-Agent-Token")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer srv.Close()

	SetExplicitHubToken(srv.URL+"/", "scion_pat_explicit")
	defer SetExplicitHubToken("", "")

	client, kind, err := createHubClient(nil, srv.URL)
	require.NoError(t, err)
	assert.Equal(t, CredentialKindHubToken, kind)
	assert.True(t, UsesUserAccessToken(kind))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = client.Health(ctx)
	assert.Equal(t, "Bearer scion_pat_explicit", gotAuth)
	assert.Empty(t, gotAgent)

	_, otherKind := selectHubCredential("https://other.example.com")
	assert.Equal(t, CredentialKindAgentToken, otherKind, "another endpoint keeps the usual order")
	assert.Empty(t, ExplicitHubToken("https://other.example.com"))
}
