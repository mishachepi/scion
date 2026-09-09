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

// Capabilities describes properties of a runtime's execution environment that
// callers would otherwise have to infer from its name.
//
// Before this type existed, call sites carried hardcoded runtime-name lists
// (`name == "docker" || name == "podman" || ...`) to decide whether a step
// applied, and runtimes that did not fit answered container-shaped questions
// with convenient lies — the tmux runtime returns true from ImageExists purely
// so that an unconditional pre-launch existence check would not try to pull an
// image it has no concept of. Both are the same defect: the caller asks the
// wrong question and the runtime cannot say "that does not apply to me".
//
// Every field here must have a real caller. A capability nobody reads is
// undetectable when it is wrong, which is how the reverted runtime_overlays
// mechanism shipped half-wired.
type Capabilities struct {
	// Images reports whether the agent's filesystem comes from a container
	// image. When false, an image name is neither required nor meaningful:
	// nothing resolves, checks or pulls one.
	Images bool

	// LocalImageStore reports whether the runtime keeps images on the machine
	// that starts the agent, so that "does this image already exist locally?"
	// is a question worth asking. Docker, Podman and Apple Container say yes;
	// Kubernetes and Cloud Run pull on the node, and a host-execution runtime
	// has no images at all.
	//
	// Implies Images: a runtime with no images has no local image store.
	LocalImageStore bool
}

// ContainerCapabilities is what every call site assumed before this type
// existed, and what a runtime that does not describe itself still gets. It
// matches Docker, Podman and Apple Container; runtimes that differ implement
// CapabilityReporter.
func ContainerCapabilities() Capabilities {
	return Capabilities{
		Images:          true,
		LocalImageStore: true,
	}
}

// CapabilityReporter is implemented by runtimes whose execution environment
// differs from the container-shaped default. It is deliberately optional, in
// the same shape as Diagnosable: adding a capability must not force an edit to
// every runtime that is unaffected by it.
type CapabilityReporter interface {
	Capabilities() Capabilities
}

// CapabilitiesOf returns rt's self-description, falling back to the
// container-shaped default for runtimes that do not implement
// CapabilityReporter.
//
// A nil runtime reports the zero value — nothing is supported. Callers guard a
// nil runtime precisely so they can skip work that would dereference it, and
// handing them container defaults would turn a skipped step into a nil panic.
func CapabilitiesOf(rt Runtime) Capabilities {
	if rt == nil {
		return Capabilities{}
	}
	if reporter, ok := rt.(CapabilityReporter); ok {
		return reporter.Capabilities()
	}
	return ContainerCapabilities()
}
