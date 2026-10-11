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

func resumeTestHarness(resumeIDFlag string) api.Harness {
	return NewDeclarativeGenericHarness(config.HarnessConfigEntry{
		Harness: "fakeharness",
		Command: &config.HarnessCommandConfig{
			Base:         []string{"fake"},
			ResumeFlag:   "--continue",
			ResumeIDFlag: resumeIDFlag,
		},
	})
}

func TestHarnessCommand_ResumeByID(t *testing.T) {
	h := resumeTestHarness("--resume {session_id}")
	cases := []struct {
		name      string
		resume    bool
		sessionID string
		want      []string
	}{
		{"resume with id pins the session", true, "uuid-1", []string{"fake", "--resume", "uuid-1", "--max", "do it"}},
		{"resume without id falls back to resume_flag", true, "", []string{"fake", "--continue", "--max", "do it"}},
		{"fresh start ignores the id", false, "uuid-1", []string{"fake", "--max", "do it"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := api.HarnessCommand(h, "do it", tc.resume, tc.sessionID, []string{"--max"})
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestHarnessCommand_NoResumeIDFlagFallsBack(t *testing.T) {
	h := resumeTestHarness("")
	got := api.HarnessCommand(h, "", true, "uuid-1", nil)
	if want := []string{"fake", "--continue"}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}
