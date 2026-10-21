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

package runtimebroker

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestStartAndRestart_ForwardProjectPreStartHook: Start re-stages the project
// pre-start hook from opts on every start and removes it when opts carries
// none, so the hook stamped at create must reach opts on start and restart
// too. Without it an agent created without starting never runs its hook, and
// any restart drops it.
func TestStartAndRestart_ForwardProjectPreStartHook(t *testing.T) {
	for _, path := range []string{"/api/v1/agents/test-agent-1/start", "/api/v1/agents/test-agent-1/restart"} {
		for _, tc := range []struct {
			name string
			body string
			want string
		}{
			{"stamped hook", `{"resolvedEnv": {"FOO": "bar"}, "projectPreStartHookScript": "#!/bin/sh\necho hi\n"}`, "#!/bin/sh\necho hi\n"},
			{"no hook", `{"resolvedEnv": {"FOO": "bar"}}`, ""},
		} {
			t.Run(path+"/"+tc.name, func(t *testing.T) {
				srv := newTestServer(t)
				mgr := srv.manager.(*mockManager)

				req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(tc.body))
				req.Header.Set("Content-Type", "application/json")
				w := httptest.NewRecorder()
				srv.Handler().ServeHTTP(w, req)

				if w.Code != http.StatusAccepted {
					t.Fatalf("expected status %d, got %d: %s", http.StatusAccepted, w.Code, w.Body.String())
				}
				if mgr.startCalls != 1 {
					t.Fatalf("expected Start to be called once, got %d", mgr.startCalls)
				}
				if got := mgr.lastStartOpts.ProjectPreStartHookScript; got != tc.want {
					t.Errorf("opts.ProjectPreStartHookScript = %q, want %q", got, tc.want)
				}
			})
		}
	}
}
