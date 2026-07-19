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

package hubclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAgentService_List_QueryParameters(t *testing.T) {
	projectID := "project-123"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		if query.Get("projectId") != projectID {
			t.Errorf("expected projectId %q, got %q", projectID, query.Get("projectId"))
		}
		if query.Get("groveId") != projectID {
			t.Errorf("expected groveId %q, got %q", projectID, query.Get("groveId"))
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"agents": []}`))
	}))
	defer server.Close()

	client, err := New(server.URL)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	_, err = client.Agents().List(context.Background(), &ListAgentsOptions{
		ProjectID: projectID,
	})
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
}

func TestListAgentsPageLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		limit := r.URL.Query().Get("limit")
		if limit != "25" {
			t.Errorf("expected limit=25 in query, got limit=%q", limit)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"agents": [], "totalCount": 0}`))
	}))
	defer server.Close()

	client, err := New(server.URL)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	opts := &ListAgentsOptions{}
	opts.Page.Limit = 25
	_, err = client.Agents().List(context.Background(), opts)
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
}

func TestListAgentsPageLimitZeroOmitted(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		limit := r.URL.Query().Get("limit")
		if limit != "" {
			t.Errorf("expected no limit param when Limit=0, got limit=%q", limit)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"agents": [], "totalCount": 0}`))
	}))
	defer server.Close()

	client, err := New(server.URL)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	_, err = client.Agents().List(context.Background(), &ListAgentsOptions{})
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
}

func TestSubscriptionService_List_QueryParameters(t *testing.T) {
	projectID := "project-123"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		if query.Get("projectId") != projectID {
			t.Errorf("expected projectId %q, got %q", projectID, query.Get("projectId"))
		}
		if query.Get("groveId") != projectID {
			t.Errorf("expected groveId %q, got %q", projectID, query.Get("groveId"))
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()

	client, err := New(server.URL)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	_, err = client.Subscriptions().List(context.Background(), &ListSubscriptionsOptions{
		ProjectID: projectID,
	})
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
}

func TestSubscriptionTemplateService_List_QueryParameters(t *testing.T) {
	projectID := "project-123"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		if query.Get("projectId") != projectID {
			t.Errorf("expected projectId %q, got %q", projectID, query.Get("projectId"))
		}
		if query.Get("groveId") != projectID {
			t.Errorf("expected groveId %q, got %q", projectID, query.Get("groveId"))
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()

	client, err := New(server.URL)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	_, err = client.SubscriptionTemplates().List(context.Background(), projectID)
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
}

func TestCreateAgentRequest_GCPIdentity_JSONRoundTrip(t *testing.T) {
	req := CreateAgentRequest{
		GCPIdentity: &GCPIdentityConfig{
			MetadataMode:     "assign",
			ServiceAccountID: "sa-123",
		},
	}
	data, err := json.Marshal(req)
	require.NoError(t, err)
	// Verify the JSON keys match what the hub expects.
	assert.Contains(t, string(data), `"metadata_mode":"assign"`)
	assert.Contains(t, string(data), `"service_account_id":"sa-123"`)
	// Round-trip.
	var decoded CreateAgentRequest
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.Equal(t, "assign", decoded.GCPIdentity.MetadataMode)
	assert.Equal(t, "sa-123", decoded.GCPIdentity.ServiceAccountID)
}

// TestAgentService_Delete_ExplicitCleanupFlags verifies that Delete always
// sends deleteFiles/removeBranch explicitly. The server must never fall back
// to its own default for these destructive flags; nil opts means
// "registration only — preserve all broker-side files".
func TestAgentService_Delete_ExplicitCleanupFlags(t *testing.T) {
	tests := []struct {
		name             string
		opts             *DeleteAgentOptions
		wantDeleteFiles  string
		wantRemoveBranch string
	}{
		{"nil opts preserves files", nil, "false", "false"},
		{"zero opts preserves files", &DeleteAgentOptions{}, "false", "false"},
		{"explicit cleanup", &DeleteAgentOptions{DeleteFiles: true, RemoveBranch: true}, "true", "true"},
		{"files only", &DeleteAgentOptions{DeleteFiles: true}, "true", "false"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var sawQuery bool
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				sawQuery = true
				query := r.URL.Query()
				if got := query.Get("deleteFiles"); got != tt.wantDeleteFiles {
					t.Errorf("deleteFiles = %q, want %q", got, tt.wantDeleteFiles)
				}
				if got := query.Get("removeBranch"); got != tt.wantRemoveBranch {
					t.Errorf("removeBranch = %q, want %q", got, tt.wantRemoveBranch)
				}
				w.WriteHeader(http.StatusNoContent)
			}))
			defer server.Close()

			client, err := New(server.URL)
			if err != nil {
				t.Fatalf("New failed: %v", err)
			}

			if err := client.Agents().Delete(context.Background(), "agent-1", tt.opts); err != nil {
				t.Fatalf("Agents().Delete failed: %v", err)
			}
			if !sawQuery {
				t.Fatal("server never saw the delete request")
			}

			if err := client.Projects().DeleteAgent(context.Background(), "project-1", "agent-1", tt.opts); err != nil {
				t.Fatalf("Projects().DeleteAgent failed: %v", err)
			}
		})
	}
}
