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

package runtimebroker

import (
	"context"

	scionrt "github.com/GoogleCloudPlatform/scion/pkg/runtime"
)

// initSignaler is implemented by runtimes whose agent init process is not
// PID 1 in the exec target. Container runtimes run `sciontool init` as PID 1
// inside the container, so `kill -USR2 1` reaches it; host-execution runtimes
// such as tmux run Exec on the broker host, where PID 1 is the host's own
// init, so the container-shaped command would signal the wrong process.
type initSignaler interface {
	SignalInit(ctx context.Context, id, signal string) error
}

// signalAgentInit delivers SIGUSR2 to the agent's sciontool init: through
// the runtime's own initSignaler when it has one, otherwise by running
// containerCmd (`kill -USR2 1`) through Exec.
func signalAgentInit(ctx context.Context, rt scionrt.Runtime, target string, containerCmd []string) error {
	if signaler, ok := rt.(initSignaler); ok {
		return signaler.SignalInit(ctx, target, "USR2")
	}
	_, err := rt.Exec(ctx, target, containerCmd)
	return err
}
