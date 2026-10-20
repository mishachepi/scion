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
	"sync"

	"github.com/GoogleCloudPlatform/scion/pkg/config"
)

// An explicit hub token is a bearer token bound to one hub endpoint for the
// whole process, set from a --hub-config file (cmd/hub_config.go). For that
// endpoint it outranks every other credential source: OAuth logins, the
// scion-token file, SCION_AUTH_TOKEN, SCION_HUB_TOKEN and dev auth. For any
// other endpoint it is never sent.
var explicitToken struct {
	mu       sync.RWMutex
	endpoint string // normalized
	token    string
}

// SetExplicitHubToken binds token to endpoint for this process. An empty
// token clears the binding.
func SetExplicitHubToken(endpoint, token string) {
	explicitToken.mu.Lock()
	defer explicitToken.mu.Unlock()
	if token == "" {
		explicitToken.endpoint, explicitToken.token = "", ""
		return
	}
	explicitToken.endpoint = config.NormalizeEndpoint(endpoint)
	explicitToken.token = token
}

// ExplicitHubToken returns the explicit token bound to endpoint, or "" when
// none is set or it is bound to another endpoint.
func ExplicitHubToken(endpoint string) string {
	explicitToken.mu.RLock()
	defer explicitToken.mu.RUnlock()
	if explicitToken.token == "" || endpoint == "" {
		return ""
	}
	if config.NormalizeEndpoint(endpoint) != explicitToken.endpoint {
		return ""
	}
	return explicitToken.token
}
