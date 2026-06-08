/*
Copyright 2025 The Scion Authors.
*/

package handlers

import (
	"strings"
	"testing"

	state "github.com/GoogleCloudPlatform/scion/pkg/agent/state"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recorderSink returns a TmuxStatusSink whose run() func records every
// invocation instead of execing tmux.
func recorderSink(t *testing.T, pane string) (*TmuxStatusSink, *[][]string) {
	t.Helper()
	var calls [][]string
	sink := &TmuxStatusSink{
		pane: pane,
		bin:  "tmux",
		run: func(bin string, args ...string) error {
			calls = append(calls, append([]string{bin}, args...))
			return nil
		},
	}
	return sink, &calls
}

func TestTmuxStatusSink_NoOpWithoutPane(t *testing.T) {
	sink, calls := recorderSink(t, "")
	sink.Apply(state.ActivityWorking)
	sink.Clear()
	assert.Empty(t, *calls, "sink must no-op when $TMUX_PANE is empty")
}

func TestTmuxStatusSink_NoOpWhenDisabledByEnv(t *testing.T) {
	t.Setenv("SCION_TMUX_STATUS", "0")
	sink, calls := recorderSink(t, "%5")
	sink.Apply(state.ActivityWorking)
	assert.Empty(t, *calls)
}

func TestTmuxStatusSink_NoOpWhenDisabledByFalse(t *testing.T) {
	t.Setenv("SCION_TMUX_STATUS", "false")
	sink, calls := recorderSink(t, "%5")
	sink.Apply(state.ActivityWorking)
	assert.Empty(t, *calls)
}

func TestTmuxStatusSink_PersistentIcon(t *testing.T) {
	sink, calls := recorderSink(t, "%5")
	sink.Apply(state.ActivityWorking)

	require.Len(t, *calls, 2)
	assert.Equal(t,
		[]string{"tmux", "set-option", "-w", "-t", "%5", TmuxStatusOption, "●"},
		(*calls)[0])
	// For non-auto-clear icons, drop any stale auto-clear hook.
	assert.Equal(t,
		[]string{"tmux", "set-hook", "-uw", "-t", "%5", tmuxStatusHookSlot},
		(*calls)[1])
}

func TestTmuxStatusSink_AutoClearIcon(t *testing.T) {
	sink, calls := recorderSink(t, "%5")
	sink.Apply(state.ActivityWaitingForInput)

	require.Len(t, *calls, 2)
	assert.Equal(t,
		[]string{"tmux", "set-option", "-w", "-t", "%5", TmuxStatusOption, "◇"},
		(*calls)[0])

	hookCall := (*calls)[1]
	require.Len(t, hookCall, 7)
	assert.Equal(t,
		[]string{"tmux", "set-hook", "-w", "-t", "%5", tmuxStatusHookSlot},
		hookCall[:6])

	body := hookCall[6]
	assert.Contains(t, body, TmuxStatusOption)
	assert.Contains(t, body, "◇")
	assert.True(t, strings.Contains(body, "set-option -uw"),
		"auto-clear hook must run set-option -uw, got: %s", body)
}

func TestTmuxStatusSink_ClearOnEmptyActivity(t *testing.T) {
	sink, calls := recorderSink(t, "%5")
	sink.Apply(state.Activity(""))

	require.Len(t, *calls, 2)
	assert.Equal(t,
		[]string{"tmux", "set-option", "-uw", "-t", "%5", TmuxStatusOption},
		(*calls)[0])
	assert.Equal(t,
		[]string{"tmux", "set-hook", "-uw", "-t", "%5", tmuxStatusHookSlot},
		(*calls)[1])
}

func TestTmuxStatusSink_ClearMethod(t *testing.T) {
	sink, calls := recorderSink(t, "%5")
	sink.Clear()

	require.Len(t, *calls, 2)
	assert.Equal(t,
		[]string{"tmux", "set-option", "-uw", "-t", "%5", TmuxStatusOption},
		(*calls)[0])
	assert.Equal(t,
		[]string{"tmux", "set-hook", "-uw", "-t", "%5", tmuxStatusHookSlot},
		(*calls)[1])
}

func TestTmuxStatusSink_AllActivitiesMapped(t *testing.T) {
	for _, a := range []state.Activity{
		state.ActivityWorking,
		state.ActivityThinking,
		state.ActivityExecuting,
		state.ActivityWaitingForInput,
		state.ActivityBlocked,
		state.ActivityCompleted,
		state.ActivityLimitsExceeded,
		state.ActivityStalled,
		state.ActivityOffline,
		state.ActivityCrashed,
	} {
		spec, ok := tmuxStatusMap[a]
		assert.True(t, ok, "activity %s has no tmux icon mapping", a)
		assert.NotEmpty(t, spec.icon, "activity %s mapped to empty icon", a)
	}
}

func TestStatusHandler_Integration_MirrorsActivityToSink(t *testing.T) {
	// Wire the recorder sink into a StatusHandler and confirm
	// UpdateActivity propagates through writeAgentInfoLocked.
	tmpDir := t.TempDir()
	sink, calls := recorderSink(t, "%5")
	h := &StatusHandler{
		StatusPath: tmpDir + "/agent-info.json",
		tmuxSink:   sink,
	}

	require.NoError(t, h.UpdateActivity(state.ActivityWaitingForInput, ""))

	require.NotEmpty(t, *calls, "JSON write should have triggered tmux mirror")
	first := (*calls)[0]
	assert.Equal(t, TmuxStatusOption, first[len(first)-2])
	assert.Equal(t, "◇", first[len(first)-1])
}
