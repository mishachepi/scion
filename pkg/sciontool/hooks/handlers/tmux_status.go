/*
Copyright 2025 The Scion Authors.
*/

package handlers

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"

	state "github.com/GoogleCloudPlatform/scion/pkg/agent/state"
)

// TmuxStatusOption is the window-level tmux user-option carrying the
// current agent activity icon. Consumers render it via a status-bar
// format snippet, e.g.:
//
//	set -g window-status-current-format \
//	  "#I:#W#{?@scion-label-scion.status, #{@scion-label-scion.status},}"
const TmuxStatusOption = "@scion-label-scion.status"

// tmuxStatusHookSlot is an indexed pane-focus-in slot so the auto-clear
// hook coexists with the user's own hooks instead of stomping them.
const tmuxStatusHookSlot = "pane-focus-in[99]"

type tmuxStatusSpec struct {
	icon      string
	autoClear bool
}

// Map covers every state.Activity. Auto-clear fires on pane-focus-in for
// states the operator visits to ack ("yes, I saw it"); persistent states
// (working/offline/crashed) stay until the next event replaces them.
var tmuxStatusMap = map[state.Activity]tmuxStatusSpec{
	state.ActivityWorking:         {"●", false},
	state.ActivityThinking:        {"◐", false},
	state.ActivityExecuting:       {"▶", false},
	state.ActivityWaitingForInput: {"◇", true},
	state.ActivityBlocked:         {"⏸", true},
	state.ActivityCompleted:       {"✓", true},
	state.ActivityLimitsExceeded:  {"⚠", true},
	state.ActivityStalled:         {"⏱", true},
	state.ActivityOffline:         {"○", false},
	state.ActivityCrashed:         {"✗", false},
}

// TmuxStatusSink mirrors agent activity to tmux user-options. Best-effort:
// no-op outside tmux, swallows errors so a broken tmux call never breaks
// the hook pipeline.
type TmuxStatusSink struct {
	pane string
	bin  string
	run  func(bin string, args ...string) error
	mu   sync.Mutex
}

// NewTmuxStatusSink reads $TMUX_PANE at construction. Each `sciontool hook`
// invocation is a fresh process, so this happens once per hook event.
func NewTmuxStatusSink() *TmuxStatusSink {
	bin := os.Getenv("SCION_TMUX_BIN")
	if bin == "" {
		bin = "tmux"
	}
	return &TmuxStatusSink{
		pane: os.Getenv("TMUX_PANE"),
		bin:  bin,
		run:  defaultRunTmux,
	}
}

func defaultRunTmux(bin string, args ...string) error {
	return exec.Command(bin, args...).Run()
}

// Enabled reports whether this sink will write to tmux. False when
// $TMUX_PANE is unset (not running inside tmux) or SCION_TMUX_STATUS is
// explicitly disabled.
func (s *TmuxStatusSink) Enabled() bool {
	if s.pane == "" {
		return false
	}
	if v := os.Getenv("SCION_TMUX_STATUS"); v == "0" || strings.EqualFold(v, "false") {
		return false
	}
	return true
}

// Apply mirrors `activity` to tmux user-options. Empty or unknown activity
// clears the option.
func (s *TmuxStatusSink) Apply(activity state.Activity) {
	if !s.Enabled() {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	spec, ok := tmuxStatusMap[activity]
	if !ok {
		s.clearLocked()
		return
	}
	_ = s.run(s.bin, "set-option", "-w", "-t", s.pane, TmuxStatusOption, spec.icon)
	if spec.autoClear {
		// Hook body compares the current option to our icon and only
		// clears if it still matches — so a later non-auto-clear icon
		// (e.g. ● working) won't be wiped by a stale waiting hook.
		hook := fmt.Sprintf(
			`if-shell -F "#{==:#{%s},%s}" "set-option -uw %s"`,
			TmuxStatusOption, spec.icon, TmuxStatusOption)
		_ = s.run(s.bin, "set-hook", "-w", "-t", s.pane, tmuxStatusHookSlot, hook)
	} else {
		// Drop any stale auto-clear hook from a prior waiting/done.
		_ = s.run(s.bin, "set-hook", "-uw", "-t", s.pane, tmuxStatusHookSlot)
	}
}

// Clear removes both the status option and the auto-clear hook.
func (s *TmuxStatusSink) Clear() {
	if !s.Enabled() {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clearLocked()
}

func (s *TmuxStatusSink) clearLocked() {
	_ = s.run(s.bin, "set-option", "-uw", "-t", s.pane, TmuxStatusOption)
	_ = s.run(s.bin, "set-hook", "-uw", "-t", s.pane, tmuxStatusHookSlot)
}
