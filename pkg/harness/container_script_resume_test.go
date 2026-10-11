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


package harness

import (
	"reflect"
	"testing"

	"github.com/GoogleCloudPlatform/scion/pkg/api"
	"github.com/GoogleCloudPlatform/scion/pkg/config"
)

var _ api.SessionResumer = (*ContainerScriptHarness)(nil)

func claudeScriptHarness() *ContainerScriptHarness {
	return &ContainerScriptHarness{entry: config.HarnessConfigEntry{
		Harness: "claude",
		Command: &config.HarnessCommandConfig{
			Base:       []string{"claude", "--no-chrome"},
			ResumeFlag: "--continue",
		},
	}}
}

// TestContainerScriptHarness_ResumePinsSessionID verifies that a script
// harness resumes the saved session by id rather than the most recent one.
func TestContainerScriptHarness_ResumePinsSessionID(t *testing.T) {
	got := api.HarnessCommand(claudeScriptHarness(), "", true, "0f1e2d3c-aaaa-bbbb-cccc-1234567890ab", []string{"--permission-mode", "bypassPermissions"})
	want := []string{"claude", "--no-chrome", "--resume", "0f1e2d3c-aaaa-bbbb-cccc-1234567890ab", "--permission-mode", "bypassPermissions"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("resume command = %q, want %q", got, want)
	}
}

// TestContainerScriptHarness_ResumeWithoutSessionIDContinues verifies the
// fallback: with no saved session id the harness resumes with resume_flag.
func TestContainerScriptHarness_ResumeWithoutSessionIDContinues(t *testing.T) {
	got := api.HarnessCommand(claudeScriptHarness(), "", true, "", nil)
	want := []string{"claude", "--no-chrome", "--continue"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("resume command = %q, want %q", got, want)
	}
}

// TestContainerScriptHarness_ResumeIDFlagFromConfig verifies that a declared
// resume_id_flag wins over the built-in one.
func TestContainerScriptHarness_ResumeIDFlagFromConfig(t *testing.T) {
	h := claudeScriptHarness()
	h.entry.Command.ResumeIDFlag = "--session {session_id}"
	got, ok := h.GetResumeCommand("next", "abc", nil)
	want := []string{"claude", "--no-chrome", "--session", "abc", "next"}
	if !ok || !reflect.DeepEqual(got, want) {
		t.Fatalf("resume command = %q ok=%v, want %q", got, ok, want)
	}
	if h.entry.Command.ResumeFlag != "--continue" {
		t.Fatalf("GetResumeCommand changed the harness's resume_flag to %q", h.entry.Command.ResumeFlag)
	}
}

// TestContainerScriptHarness_NoResumeIDFlagForUnknownHarness verifies that a
// harness with no id flag reports false, so the caller uses resume_flag.
func TestContainerScriptHarness_NoResumeIDFlagForUnknownHarness(t *testing.T) {
	h := claudeScriptHarness()
	h.entry.Harness = "testcli"
	if _, ok := h.GetResumeCommand("", "abc", nil); ok {
		t.Fatal("GetResumeCommand reported a pinned resume for a harness without a resume id flag")
	}
}
