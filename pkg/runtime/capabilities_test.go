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

package runtime

import "testing"

// A runtime that says nothing about itself must keep looking exactly like the
// container runtimes every call site was written against. If this drifts,
// adding a capability silently changes behaviour for Docker/Podman/Apple.
func TestCapabilitiesOf_DefaultsToContainerShape(t *testing.T) {
	for _, rt := range []Runtime{
		&DockerRuntime{},
		&PodmanRuntime{},
		&AppleContainerRuntime{},
		&MockRuntime{},
	} {
		got := CapabilitiesOf(rt)
		if got != ContainerCapabilities() {
			t.Errorf("%s: got %+v, want the container-shaped default %+v",
				rt.Name(), got, ContainerCapabilities())
		}
	}
}

// A nil runtime supports nothing. Callers guard nil so they can skip work that
// would dereference it; handing them container defaults would convert a
// skipped step into a nil panic.
func TestCapabilitiesOf_NilRuntimeSupportsNothing(t *testing.T) {
	if got := CapabilitiesOf(nil); got != (Capabilities{}) {
		t.Errorf("got %+v, want the zero value", got)
	}
}

func TestCapabilitiesOf_Reporters(t *testing.T) {
	tests := []struct {
		name string
		rt   Runtime
		want Capabilities
	}{
		{
			// Host execution: no image behind an agent at all.
			name: "tmux",
			rt:   NewTmuxRuntime(),
			want: Capabilities{Images: false, LocalImageStore: false},
		},
		{
			// Images exist, but the node pulls them — a local check on the
			// machine running scion answers nothing.
			name: "kubernetes",
			rt:   &KubernetesRuntime{},
			want: Capabilities{Images: true, LocalImageStore: false},
		},
		{
			name: "cloudrun",
			rt:   &CloudRunRuntime{},
			want: Capabilities{Images: true, LocalImageStore: false},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := CapabilitiesOf(tc.rt); got != tc.want {
				t.Errorf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

// LocalImageStore replaced a hardcoded name list in pkg/agent/run.go
// ("docker" || "podman" || "container" || "apple-container"). This pins the
// refactor to the same answer for every runtime the list covered, so a
// behaviour change would have to be deliberate rather than incidental.
//
// Note the list carried a dead entry: AppleContainerRuntime.Name() is
// "container", never "apple-container" — the kind of silent drift a
// self-describing runtime cannot produce.
func TestCapabilities_LocalImageStoreMatchesReplacedNameList(t *testing.T) {
	legacyNameList := map[string]bool{
		"docker":          true,
		"podman":          true,
		"container":       true,
		"apple-container": true,
		"kubernetes":      false,
		"cloudrun":        false,
		"tmux":            false,
	}

	for _, rt := range []Runtime{
		&DockerRuntime{},
		&PodmanRuntime{},
		&AppleContainerRuntime{},
		&KubernetesRuntime{},
		&CloudRunRuntime{},
		NewTmuxRuntime(),
	} {
		want, known := legacyNameList[rt.Name()]
		if !known {
			t.Fatalf("runtime %q is not covered by this pin — extend the table deliberately", rt.Name())
		}
		if got := CapabilitiesOf(rt).LocalImageStore; got != want {
			t.Errorf("%s: LocalImageStore=%v, legacy name list said %v", rt.Name(), got, want)
		}
	}
}

// Images and LocalImageStore are not independent: a runtime with no images
// cannot have a local store of them.
func TestCapabilities_LocalImageStoreImpliesImages(t *testing.T) {
	for _, rt := range []Runtime{
		&DockerRuntime{},
		&PodmanRuntime{},
		&AppleContainerRuntime{},
		&KubernetesRuntime{},
		&CloudRunRuntime{},
		NewTmuxRuntime(),
	} {
		caps := CapabilitiesOf(rt)
		if caps.LocalImageStore && !caps.Images {
			t.Errorf("%s: LocalImageStore without Images: %+v", rt.Name(), caps)
		}
	}
}
