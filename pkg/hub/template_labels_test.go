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

package hub

import (
	"testing"

	"github.com/GoogleCloudPlatform/scion/pkg/config"
	"github.com/GoogleCloudPlatform/scion/pkg/store"
)

// TestMergeTemplateLabels_FromParsedAgentConfig covers the whole path of a
// template-declared default label: scion-agent.yaml -> the derived AgentConfig
// snapshot -> the labels stamped onto an agent created from the template.
func TestMergeTemplateLabels_FromParsedAgentConfig(t *testing.T) {
	cfg, err := config.ParseScionAgentConfig("scion-agent.yaml", []byte(`
schema_version: "1"
default_harness_config: claude
labels:
  tier: batch
  team: platform
`))
	if err != nil {
		t.Fatalf("ParseScionAgentConfig: %v", err)
	}
	tmpl := &store.Template{AgentConfig: cfg}

	got := mergeTemplateLabels(map[string]string{"team": "request"}, tmpl)
	if got["tier"] != "batch" {
		t.Errorf("template label tier missing: %v", got)
	}
	if got["team"] != "request" {
		t.Errorf("request label must win: team=%q", got["team"])
	}
}
