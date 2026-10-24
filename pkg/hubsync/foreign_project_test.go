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

package hubsync

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/GoogleCloudPlatform/scion/pkg/config"
	"github.com/stretchr/testify/assert"
)

func TestIsForeignProject(t *testing.T) {
	const own, other = "own-id", "other-id"

	linkedDir := filepath.Join(t.TempDir(), ".scion")
	if err := os.MkdirAll(linkedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(linkedDir, "settings.yaml"), []byte("hub:\n  project_id: "+own+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	unlinkedDir := filepath.Join(t.TempDir(), ".scion")
	if err := os.MkdirAll(unlinkedDir, 0o755); err != nil {
		t.Fatal(err)
	}

	withLink := &config.Settings{Hub: &config.HubClientConfig{ProjectID: own}}
	tests := []struct {
		name     string
		settings *config.Settings
		path     string
		global   bool
		id       string
		want     bool
	}{
		{"nil settings", nil, linkedDir, false, own, true},
		{"own project_id", &config.Settings{ProjectID: own}, unlinkedDir, false, own, false},
		{"own hub link", withLink, linkedDir, false, own, false},
		{"hub link of the global project", withLink, unlinkedDir, true, own, false},
		{"hub link inherited from global settings", withLink, unlinkedDir, false, own, true},
		{"other project", withLink, linkedDir, false, other, true},
		{"empty target id", withLink, linkedDir, false, "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, isForeignProject(tc.settings, tc.path, tc.global, tc.id))
		})
	}
}
