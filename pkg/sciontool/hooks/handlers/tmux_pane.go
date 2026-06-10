/*
Copyright 2025 The Scion Authors.
*/

package handlers

import (
	"os"
	"os/exec"
	"strings"
	"sync"
)

// TmuxPaneOption is the window-level tmux user-option carrying the agent's
// pane_id. Consumers (scion message routing, ccgram, etc.) read it via:
//
//	tmux show-option -wv -t <window> @scion-pane
//
// Kept in lockstep with tmuxPaneOption in pkg/runtime/tmux.go — duplicated
// here so the hook package doesn't depend on the runtime package.
const TmuxPaneOption = "@scion-pane"

// TmuxPaneSink refreshes @scion-pane to the hook's current $TMUX_PANE.
// Called on SessionStart so the routing target survives tmux server restart
// (new pane_ids) and user-driven pane swaps. Best-effort: no-op outside tmux,
// swallows errors so a broken tmux call never breaks the hook pipeline.
type TmuxPaneSink struct {
	pane string
	bin  string
	run  func(bin string, args ...string) error
	mu   sync.Mutex
}

// NewTmuxPaneSink reads $TMUX_PANE at construction. Each `sciontool hook`
// invocation is a fresh process, so this happens once per hook event.
func NewTmuxPaneSink() *TmuxPaneSink {
	bin := os.Getenv("SCION_TMUX_BIN")
	if bin == "" {
		bin = "tmux"
	}
	return &TmuxPaneSink{
		pane: os.Getenv("TMUX_PANE"),
		bin:  bin,
		run: func(bin string, args ...string) error {
			return exec.Command(bin, args...).Run()
		},
	}
}

// Enabled mirrors TmuxStatusSink: false when $TMUX_PANE is unset or
// SCION_TMUX_STATUS is explicitly disabled (same kill-switch covers both).
func (s *TmuxPaneSink) Enabled() bool {
	if s.pane == "" {
		return false
	}
	if v := os.Getenv("SCION_TMUX_STATUS"); v == "0" || strings.EqualFold(v, "false") {
		return false
	}
	return true
}

// Apply writes the current pane_id to the window's @scion-pane user-option.
// tmux resolves "-t %N" (a pane id) to the containing window for -w options.
func (s *TmuxPaneSink) Apply() {
	if !s.Enabled() {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.run(s.bin, "set-option", "-w", "-t", s.pane, TmuxPaneOption, s.pane)
}
