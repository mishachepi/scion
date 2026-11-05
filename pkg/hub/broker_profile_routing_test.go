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
	"context"
	"net/http/httptest"
	"testing"

	"github.com/GoogleCloudPlatform/scion/pkg/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestResolveRuntimeBrokerForProfile: a create naming a profile goes to the
// project's default broker when it offers the profile, else to the only
// broker that does; without such a broker, or with a broker named, the
// usual resolution applies.
func TestResolveRuntimeBrokerForProfile(t *testing.T) {
	f := newBrokerPresenceFixture(t)
	setConduitExperiment(t, f.srv, false)
	withProfiles := func(names ...string) func(*store.RuntimeBroker) {
		return func(b *store.RuntimeBroker) {
			b.AutoProvide = true
			for _, n := range names {
				b.Profiles = append(b.Profiles, store.BrokerProfile{Name: n, Type: "tmux", Available: true})
			}
		}
	}
	tmuxHost := f.broker(t, "profile-route-tmux", store.BrokerStatusOnline, withProfiles("local", "tmux-local"))
	dockerHost := f.broker(t, "profile-route-docker", store.BrokerStatusOnline, withProfiles("local", "docker"))
	other := f.broker(t, "profile-route-other", store.BrokerStatusOnline, withProfiles("local", "shared"))
	twin := f.broker(t, "profile-route-twin", store.BrokerStatusOnline, withProfiles("shared"))
	p := f.placementPresenceProject(t, "profile-route-project", tmuxHost, dockerHost, other, twin)
	p.DefaultRuntimeBrokerID = tmuxHost.ID
	require.NoError(t, f.store.UpdateProject(context.Background(), p))

	ctx := contextWithIdentity(context.Background(), NewDevUser(DevUserConfig{
		Username: "dev", DisplayName: "Development User", Email: "dev@localhost",
	}))
	resolve := func(requested, profile string) string {
		t.Helper()
		w := httptest.NewRecorder()
		got, err := f.srv.resolveRuntimeBrokerForProfile(ctx, w, requested, p, profile)
		require.NoError(t, err, w.Body.String())
		return got
	}

	assert.Equal(t, dockerHost.ID, resolve("", "docker"), "only the docker host offers docker")
	assert.Equal(t, tmuxHost.ID, resolve("", "local"), "the default broker offers local")
	assert.Equal(t, tmuxHost.ID, resolve("", ""), "no profile: the default broker")
	assert.Equal(t, tmuxHost.ID, resolve("", "shared"), "two brokers offer it and neither is the default: the default, which refuses")
	assert.Equal(t, tmuxHost.ID, resolve("", "absent"), "nobody offers it: the default, which refuses")
	assert.Equal(t, tmuxHost.ID, resolve(tmuxHost.ID, "docker"), "a named broker is kept")
}
