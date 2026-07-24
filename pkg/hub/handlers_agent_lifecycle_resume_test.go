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

//go:build !no_sqlite

package hub

import (
	"context"
	"testing"

	"github.com/GoogleCloudPlatform/scion/pkg/storage"
	"github.com/GoogleCloudPlatform/scion/pkg/store"
)

// TestHarnessSupportsResume_InstalledConfig verifies that the suspend
// capability check consults installed harness-configs (hub store) before the
// compiled-in embeds. Without this, a user-installed harness-config such as
// claude-tmux silently resolves to Generic (resume unsupported) and suspend
// is rejected even though the config declares resume support.
func TestHarnessSupportsResume_InstalledConfig(t *testing.T) {
	srv, _, _ := testTemplateBootstrapServer(t)
	ctx := context.Background()

	install := func(t *testing.T, name, configYAML string) {
		t.Helper()
		br := testBundledResource(storage.ResourceKindHarnessConfig, name, map[string]string{
			"config.yaml": configYAML,
		})
		rs := srv.harnessConfigStore("claude")
		if _, err := rs.BootstrapSource(ctx, NewFSResourceSource(br), BootstrapOptions{
			OverwritePolicy: OverwriteBuiltinManaged,
		}); err != nil {
			t.Fatalf("bootstrap %s failed: %v", name, err)
		}
	}

	install(t, "hc-resume-yes",
		"harness: claude\ncommand:\n  resume_flag: \"--continue\"\ncapabilities:\n  resume: { support: \"yes\" }\n")
	install(t, "hc-resume-no",
		"harness: claude\ncapabilities:\n  resume: { support: \"no\", reason: \"no session store\" }\n")
	install(t, "hc-resume-undeclared", "harness: claude\n")

	agentWith := func(hc string) *store.Agent {
		return &store.Agent{AppliedConfig: &store.AgentAppliedConfig{HarnessConfig: hc}}
	}

	if ok, _ := srv.harnessSupportsResume(ctx, agentWith("hc-resume-yes")); !ok {
		t.Error("installed config declaring resume support=yes must be resumable")
	}
	if ok, reason := srv.harnessSupportsResume(ctx, agentWith("hc-resume-no")); ok {
		t.Error("installed config declaring resume support=no must not be resumable")
	} else if reason != "no session store" {
		t.Errorf("reason = %q, want the declared reason", reason)
	}
	if ok, _ := srv.harnessSupportsResume(ctx, agentWith("hc-resume-undeclared")); !ok {
		t.Error("installed config without a capability matrix must default to resumable")
	}
	// Not installed and not an embedded harness: falls back to Generic,
	// which does not support resume.
	if ok, _ := srv.harnessSupportsResume(ctx, agentWith("hc-not-installed")); ok {
		t.Error("unknown harness must fall back to Generic (no resume)")
	}
}
