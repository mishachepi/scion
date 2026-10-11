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
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/GoogleCloudPlatform/scion/pkg/api"
	"github.com/GoogleCloudPlatform/scion/pkg/config"
)

// GetSavedHarnessSessionID returns the harness session id captured into
// agent-info.json by sciontool's SessionStart hook, or "" when none was
// captured (fresh agent, or a harness that does not report one). A resume
// uses it to request that exact session (api.SessionResumer).
func GetSavedHarnessSessionID(agentName string, projectPath string) string {
	projectDir, err := config.GetResolvedProjectDir(projectPath)
	if err != nil {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(config.GetAgentHomePath(projectDir, agentName), "agent-info.json"))
	if err != nil {
		return ""
	}
	var info api.AgentInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return ""
	}
	return info.HarnessSessionID
}
