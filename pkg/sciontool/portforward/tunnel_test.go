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

package portforward

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	scionhub "github.com/GoogleCloudPlatform/scion/pkg/sciontool/hub"
)

func TestIsLoopbackHost(t *testing.T) {
	tests := []struct {
		host string
		want bool
	}{
		{"localhost", true},
		{"LOCALHOST", true},
		{" localhost ", true},
		{"127.0.0.1", true},
		{"::1", true},
		{"127.0.0.2", true},
		{"10.0.0.1", false},
		{"169.254.169.254", false},
		{"0.0.0.0", false},
		{"example.com", false},
		{"localhost.evil.com", false},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(tt.host, func(t *testing.T) {
			if got := isLoopbackHost(tt.host); got != tt.want {
				t.Errorf("isLoopbackHost(%q) = %v, want %v", tt.host, got, tt.want)
			}
		})
	}
}

func jwtWithExpiry(t *testing.T, exp time.Time) string {
	t.Helper()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	payloadJSON, err := json.Marshal(struct {
		Exp int64 `json:"exp"`
	}{Exp: exp.Unix()})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return header + "." + base64.RawURLEncoding.EncodeToString(payloadJSON) + ".sig"
}

// A hub that rejects the tunnel's token answers the websocket upgrade with a
// plain 401, which gorilla surfaces only as "bad handshake". Without adoption
// the tunnel redials with the same dead token forever, even once a recovery
// has written a live one to the canonical file.
func TestTunnel_AdoptsTokenFileOnUnauthorizedHandshake(t *testing.T) {
	cleanup := scionhub.SetTokenHome(t.TempDir())
	defer cleanup()

	staleToken := jwtWithExpiry(t, time.Now().Add(10*time.Hour))
	freshToken := jwtWithExpiry(t, time.Now().Add(11*time.Hour))

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	client := scionhub.NewClientWithConfig(server.URL, staleToken, "agent-1")
	if err := scionhub.WriteTokenFile(freshToken); err != nil {
		t.Fatalf("write token file: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := NewManager(client).runOnce(ctx); err == nil {
		t.Fatal("runOnce should report the failed handshake")
	}
	if got := client.GetToken(); got != freshToken {
		t.Errorf("tunnel still holds %q; want the token adopted from disk", got)
	}
}

// A handshake that fails for any other reason must leave the token alone.
func TestTunnel_KeepsTokenOnNonAuthHandshakeFailure(t *testing.T) {
	cleanup := scionhub.SetTokenHome(t.TempDir())
	defer cleanup()

	staleToken := jwtWithExpiry(t, time.Now().Add(10*time.Hour))
	freshToken := jwtWithExpiry(t, time.Now().Add(11*time.Hour))

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	client := scionhub.NewClientWithConfig(server.URL, staleToken, "agent-1")
	if err := scionhub.WriteTokenFile(freshToken); err != nil {
		t.Fatalf("write token file: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := NewManager(client).runOnce(ctx); err == nil {
		t.Fatal("runOnce should report the failed handshake")
	}
	if got := client.GetToken(); got != staleToken {
		t.Errorf("a 500 must not trigger token adoption; token changed to %q", got)
	}
}
