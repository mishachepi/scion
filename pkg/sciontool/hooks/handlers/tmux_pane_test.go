/*
Copyright 2025 The Scion Authors.
*/

package handlers

import (
	"testing"

	"github.com/GoogleCloudPlatform/scion/pkg/sciontool/hooks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func recorderPaneSink(t *testing.T, pane string) (*TmuxPaneSink, *[][]string) {
	t.Helper()
	var calls [][]string
	sink := &TmuxPaneSink{
		pane: pane,
		bin:  "tmux",
		run: func(bin string, args ...string) error {
			calls = append(calls, append([]string{bin}, args...))
			return nil
		},
	}
	return sink, &calls
}

func TestTmuxPaneSink_NoOpWithoutPane(t *testing.T) {
	sink, calls := recorderPaneSink(t, "")
	sink.Apply()
	assert.Empty(t, *calls, "Apply must no-op when $TMUX_PANE is empty")
}

func TestTmuxPaneSink_NoOpWhenDisabled(t *testing.T) {
	t.Setenv("SCION_TMUX_STATUS", "0")
	sink, calls := recorderPaneSink(t, "%17")
	sink.Apply()
	assert.Empty(t, *calls)
}

func TestTmuxPaneSink_WritesPaneIdToWindow(t *testing.T) {
	sink, calls := recorderPaneSink(t, "%17")
	sink.Apply()

	require.Len(t, *calls, 1)
	assert.Equal(t,
		[]string{"tmux", "set-option", "-w", "-t", "%17", TmuxPaneOption, "%17"},
		(*calls)[0])
}

func TestStatusHandler_SessionStart_RefreshesPaneSink(t *testing.T) {
	tmpDir := t.TempDir()
	statusSink, statusCalls := recorderSink(t, "%17")
	paneSink, paneCalls := recorderPaneSink(t, "%17")
	h := &StatusHandler{
		StatusPath:   tmpDir + "/agent-info.json",
		tmuxSink:     statusSink,
		tmuxPaneSink: paneSink,
	}

	err := h.Handle(&hooks.Event{Name: hooks.EventSessionStart, Dialect: "claude"})
	require.NoError(t, err)

	// SessionStart hits the pane sink exactly once with the current pane_id.
	require.Len(t, *paneCalls, 1)
	assert.Equal(t,
		[]string{"tmux", "set-option", "-w", "-t", "%17", TmuxPaneOption, "%17"},
		(*paneCalls)[0])

	// And the status sink still fires through the activity path
	// (SessionStart maps to working).
	require.NotEmpty(t, *statusCalls)
}

func TestStatusHandler_NonSessionStart_DoesNotRefreshPaneSink(t *testing.T) {
	tmpDir := t.TempDir()
	paneSink, paneCalls := recorderPaneSink(t, "%17")
	h := &StatusHandler{
		StatusPath:   tmpDir + "/agent-info.json",
		tmuxPaneSink: paneSink,
	}

	err := h.Handle(&hooks.Event{Name: hooks.EventToolStart, Dialect: "claude"})
	require.NoError(t, err)
	assert.Empty(t, *paneCalls, "pane sink must only fire on SessionStart")
}
