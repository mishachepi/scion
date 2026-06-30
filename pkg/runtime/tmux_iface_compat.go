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

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

// ExecWithStdin runs cmd on the broker host with stdin piped from the given
// reader, satisfying Runtime.ExecWithStdin (#1355: the broker's reset-auth
// path must not put the token on argv). Minimal form: no agent identity or
// pane cwd yet — Exec's host-execution semantics are layered on later.
func (r *TmuxRuntime) ExecWithStdin(ctx context.Context, id string, cmd []string, stdin io.Reader) (string, error) {
	if id == "" {
		return "", fmt.Errorf("tmux runtime: ExecWithStdin requires a non-empty id")
	}
	if len(cmd) == 0 {
		return "", fmt.Errorf("tmux runtime: ExecWithStdin requires a non-empty command")
	}
	c := exec.CommandContext(ctx, cmd[0], cmd[1:]...)
	c.Stdin = stdin
	out, err := c.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("exec %v on %s: %w (output: %s)",
			cmd, id, err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}
