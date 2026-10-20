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

package cmd

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/GoogleCloudPlatform/scion/pkg/config"
	"github.com/GoogleCloudPlatform/scion/pkg/hubsync"
)

// Hub config files (--hub-config, SCION_HUB_CONFIG) point one invocation at
// a hub, a project on it and a token for it, so that an agent of one hub can
// list and message the agents of another hub without -g/--hub on every
// command:
//
//	hub:
//	  endpoint: https://hub.example.com
//	  project_id: 0b8d33c9-...
//	auth:
//	  token_file: /home/me/.scion/tokens/hub.pat
//
// The file holds no secret: token_file is an absolute path to a file that
// holds the token (a user access token, scion_pat_...), readable by its
// owner only. With a token, the invocation acts as the token's principal:
// the token outranks every other credential for that endpoint, and the
// local agent identity (agent token, agent name, own project and hub) is
// dropped for this process, so none of it reaches the other hub.

const envHubConfig = "SCION_HUB_CONFIG"

// hubConfigFlag is the --hub-config value.
var hubConfigFlag string

var (
	// hubConfigApplied is the path of the hub config file applyHubConfig
	// applied in this invocation, or "". With it set, hubEndpoint came from
	// that file.
	hubConfigApplied string
	// projectFromHubConfig is true when applyHubConfig set projectPath from
	// the file's hub.project_id.
	projectFromHubConfig bool
)

// hubConfigFile is the schema of a hub config file.
type hubConfigFile struct {
	// Target is the file's hub section. The Go name is not Hub so that it
	// is not mistaken for the retired settings key hub.endpoint.
	Target struct {
		Endpoint  string `yaml:"endpoint"`
		ProjectID string `yaml:"project_id"`
	} `yaml:"hub"`
	Auth struct {
		TokenFile string `yaml:"token_file"`
	} `yaml:"auth"`
}

// agentIdentityEnv is the environment that makes the CLI act as the local
// agent on its own hub. It is dropped when a hub config supplies a token.
var agentIdentityEnv = []string{
	"SCION_AGENT_NAME", "SCION_AGENT_SLUG", "SCION_AGENT_ID",
	"SCION_PROJECT", "SCION_PROJECT_ID", "SCION_HUB_PROJECT_ID",
	"SCION_HUB_ENDPOINT", "SCION_HUB_URL",
	"SCION_AUTH_TOKEN", "SCION_HUB_TOKEN", "SCION_HUB_API_KEY",
}

func init() {
	rootCmd.PersistentFlags().StringVar(&hubConfigFlag, "hub-config", "",
		"Hub config file (hub.endpoint, hub.project_id, auth.token_file) selecting the hub, project and token for this command (env: "+envHubConfig+")")
}

// hubConfigPath returns --hub-config, else SCION_HUB_CONFIG, else "".
func hubConfigPath() string {
	if hubConfigFlag != "" {
		return hubConfigFlag
	}
	return strings.TrimSpace(os.Getenv(envHubConfig))
}

// loadHubConfigFile reads and validates a hub config file.
func loadHubConfigFile(path string) (*hubConfigFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("hub config: %w", err)
	}
	var c hubConfigFile
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("hub config %s: %w", path, err)
	}
	c.Target.Endpoint = strings.TrimSpace(c.Target.Endpoint)
	c.Target.ProjectID = strings.TrimSpace(c.Target.ProjectID)
	c.Auth.TokenFile = strings.TrimSpace(c.Auth.TokenFile)
	if c.Target.Endpoint == "" {
		return nil, fmt.Errorf("hub config %s: hub.endpoint is required", path)
	}
	u, err := url.Parse(c.Target.Endpoint)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("hub config %s: hub.endpoint %q is not an http(s) URL", path, config.RedactURL(c.Target.Endpoint))
	}
	if c.Auth.TokenFile != "" && !filepath.IsAbs(c.Auth.TokenFile) {
		return nil, fmt.Errorf("hub config %s: auth.token_file must be an absolute path (agents run with their own HOME)", path)
	}
	return &c, nil
}

// readHubConfigToken reads the token from path. The file must be a regular
// file readable by its owner only.
func readHubConfigToken(path string) (string, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("hub config token_file: %w", err)
	}
	if !fi.Mode().IsRegular() {
		return "", fmt.Errorf("hub config token_file %s is not a regular file", path)
	}
	if fi.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("hub config token_file %s is readable by others (mode %04o); run: chmod 600 %s", path, fi.Mode().Perm(), path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("hub config token_file: %w", err)
	}
	token := strings.TrimSpace(string(data))
	if token == "" {
		return "", fmt.Errorf("hub config token_file %s is empty", path)
	}
	return token, nil
}

// errHubConfigWithProfile refuses --hub-config together with a hub profile.
var errHubConfigWithProfile = errors.New("--hub-config cannot be combined with --hub-profile")

// errUseWithHubConfig refuses to change the saved context while a hub
// config selects the hub: the hub config applies to one command only.
var errUseWithHubConfig = errors.New("'scion use' cannot change the saved context while a hub config (--hub-config or " + envHubConfig + ") selects the hub")

// applyHubConfig applies the selected hub config file, if any, to this
// invocation. It runs from root's PersistentPreRunE before the hub and
// project are resolved from the context:
//
//   - hub.endpoint selects the hub; an explicit --hub must name the same hub;
//   - hub.project_id selects the project unless --project or --global does;
//   - auth.token_file binds its token to the endpoint (it outranks every
//     other credential there) and drops the local agent identity from the
//     environment of this process.
//
// Without a token, an agent may not use a hub config that names a hub other
// than its own: its credentials belong to its own hub and must not be sent
// elsewhere.
func applyHubConfig() error {
	hubConfigApplied, projectFromHubConfig = "", false
	path := hubConfigPath()
	if path == "" {
		return nil
	}
	if hubProfileSelected() {
		return errHubConfigWithProfile
	}
	c, err := loadHubConfigFile(path)
	if err != nil {
		return err
	}
	if hubEndpoint != "" && !isHubNickname(hubEndpoint) &&
		config.NormalizeEndpoint(hubEndpoint) != config.NormalizeEndpoint(c.Target.Endpoint) {
		return fmt.Errorf("--hub %s does not match hub.endpoint %s in %s",
			config.RedactURL(hubEndpoint), config.RedactURL(c.Target.Endpoint), path)
	}
	if hubEndpoint != "" && isHubNickname(hubEndpoint) {
		return fmt.Errorf("--hub %s cannot be combined with --hub-config; the hub config names its hub", hubEndpoint)
	}

	if c.Auth.TokenFile == "" {
		if own, _ := envHubEndpoint(); (isInAgentContext() || config.IsHubManagedAgent()) &&
			config.NormalizeEndpoint(own) != config.NormalizeEndpoint(c.Target.Endpoint) {
			return fmt.Errorf("hub config %s: auth.token_file is required to reach %s from an agent; the agent's own credentials are for its own hub",
				path, config.RedactURL(c.Target.Endpoint))
		}
	} else {
		token, err := readHubConfigToken(c.Auth.TokenFile)
		if err != nil {
			return err
		}
		for _, name := range agentIdentityEnv {
			_ = os.Unsetenv(name)
		}
		hubsync.SetExplicitHubToken(c.Target.Endpoint, token)
	}

	hubEndpoint = c.Target.Endpoint
	hubConfigApplied = path
	if projectPath == "" && !globalMode && c.Target.ProjectID != "" {
		projectPath = c.Target.ProjectID
		projectFromHubConfig = true
	}
	return nil
}
