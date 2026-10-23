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

func TestAppleRunContainerID(t *testing.T) {
	tests := []struct {
		name string
		out  string
		want string
	}{
		{"bare id", "m5--agent\n", "m5--agent"},
		{"progress lines before the id (CLI 1.6, -t)", "[0/6] [0s]\n[1/6] Fetching image [0s]\n[6/6] Starting container [1s]\nm5--agent\n", "m5--agent"},
		{"carriage-return progress updates", "[6/6] Starting container [0s]\r[6/6] Starting container [1s]\rm5--agent\r\n", "m5--agent"},
		{"trailing blank lines", "m5--agent\n\n  \n", "m5--agent"},
		{"empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := appleRunContainerID(tt.out); got != tt.want {
				t.Errorf("appleRunContainerID(%q) = %q, want %q", tt.out, got, tt.want)
			}
		})
	}
}
