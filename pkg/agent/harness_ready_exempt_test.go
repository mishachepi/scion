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

package agent

import (
	"context"
	"testing"
	"time"

	"github.com/GoogleCloudPlatform/scion/pkg/runtime"
)

// exemptReadyRuntime is a runtime that opts out of the readiness protocol,
// as the tmux runtime does.
type exemptReadyRuntime struct {
	*fakeReadyRuntime
}

func (exemptReadyRuntime) HarnessReadinessExempt() bool { return true }

// TestWaitHarnessReady_ExemptRuntimeDoesNotProbe verifies that a runtime
// exempt from the readiness protocol is never probed and the wait returns at
// once, even when a probe would report not ready until the deadline.
func TestWaitHarnessReady_ExemptRuntimeDoesNotProbe(t *testing.T) {
	rt := exemptReadyRuntime{newFakeReadyRuntime(func(int) (string, error) { return hookProbe(false), nil })}
	timings := fastReadyTimings
	timings.Deadline = 2 * time.Second
	mgr := &AgentManager{Runtime: rt, readyTimings: timings}

	start := time.Now()
	mgr.waitHarnessReady(context.Background(), "test-agent", "")
	if el := time.Since(start); el > 500*time.Millisecond {
		t.Fatalf("waited %v, want an immediate return", el)
	}
	if probes, _ := rt.snapshot(); probes != 0 {
		t.Fatalf("probes = %d, want 0 for an exempt runtime", probes)
	}
}

// hostWorkspaceRuntime is a runtime whose harness runs in the host
// workspace directory, as the tmux runtime's does.
type hostWorkspaceRuntime struct {
	*fakeReadyRuntime
}

func (hostWorkspaceRuntime) RunsHarnessInHostWorkspace() bool { return true }

// TestProvisionManifestWorkspace verifies the workspace a container-script
// provisioner is handed: the container mount point in a container, the host
// workspace (or the agent home without one) when the harness runs on the host.
func TestProvisionManifestWorkspace(t *testing.T) {
	container := newFakeReadyRuntime(nil)
	host := hostWorkspaceRuntime{newFakeReadyRuntime(nil)}
	cases := []struct {
		name          string
		rt            runtime.Runtime
		hostWorkspace string
		want          string
	}{
		{"container", container, "/srv/agents/a/workspace", "/workspace"},
		{"host workspace", host, "/srv/agents/a/workspace", "/srv/agents/a/workspace"},
		{"host without workspace", host, "", "/srv/agents/a/home"},
	}
	for _, c := range cases {
		got := provisionManifestWorkspace(c.rt, "/workspace", c.hostWorkspace, "/srv/agents/a/home")
		if got != c.want {
			t.Errorf("%s: workspace = %q, want %q", c.name, got, c.want)
		}
	}
}
