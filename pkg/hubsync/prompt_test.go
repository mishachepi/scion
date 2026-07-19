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
	"os"
	"testing"
)

func TestConfirmAction_AutoConfirm(t *testing.T) {
	tests := []struct {
		name       string
		defaultYes bool
	}{
		{"defaultYes=true", true},
		{"defaultYes=false", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ConfirmAction("Test prompt", tt.defaultYes, true)
			if !result {
				t.Errorf("ConfirmAction with autoConfirm=true should always return true, got false (defaultYes=%v)", tt.defaultYes)
			}
		})
	}
}

func TestConfirmAction_NoAutoConfirm_EOFDeclines(t *testing.T) {
	// When not auto-confirming and stdin yields no input (EOF — running
	// non-interactively), the prompt must DECLINE regardless of defaultYes.
	// The interactive default must never confirm an action nobody saw.
	for _, defaultYes := range []bool{true, false} {
		if result := ConfirmAction("Test prompt", defaultYes, false); result {
			t.Errorf("ConfirmAction(defaultYes=%v) should return false on stdin EOF", defaultYes)
		}
	}
}

// withStdin replaces os.Stdin with a pipe carrying the given input for the
// duration of fn.
func withStdin(t *testing.T, input string, fn func()) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	orig := os.Stdin
	os.Stdin = r
	defer func() {
		os.Stdin = orig
		_ = r.Close()
	}()
	if _, err := w.WriteString(input); err != nil {
		t.Fatalf("write stdin: %v", err)
	}
	_ = w.Close()
	fn()
}

func TestConfirmAction_InteractiveInputs(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		defaultYes bool
		want       bool
	}{
		{"empty line uses default yes", "\n", true, true},
		{"empty line uses default no", "\n", false, false},
		{"explicit yes", "y\n", false, true},
		{"explicit no", "n\n", true, false},
		{"partial yes without newline", "y", false, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withStdin(t, tt.input, func() {
				if got := ConfirmAction("Test prompt", tt.defaultYes, false); got != tt.want {
					t.Errorf("ConfirmAction(input=%q, defaultYes=%v) = %v, want %v", tt.input, tt.defaultYes, got, tt.want)
				}
			})
		})
	}
}

func TestShowSyncPlan_RemovalsDefaultToNo(t *testing.T) {
	// A bare Enter must not confirm a sync that would remove hub
	// registrations; register-only syncs keep the friendly Yes default.
	removal := &SyncResult{ToRemove: []AgentRef{{Name: "gone-agent", ID: "id-1"}}}
	withStdin(t, "\n", func() {
		if ShowSyncPlan(removal, false) {
			t.Error("ShowSyncPlan with removals should decline on bare Enter")
		}
	})

	registerOnly := &SyncResult{ToRegister: []string{"new-agent"}}
	withStdin(t, "\n", func() {
		if !ShowSyncPlan(registerOnly, false) {
			t.Error("ShowSyncPlan with register-only plan should confirm on bare Enter")
		}
	})
}

func TestNextSlugFromMatches(t *testing.T) {
	tests := []struct {
		name     string
		baseSlug string
		matches  []ProjectMatch
		want     string
	}{
		{
			name:     "no matches",
			baseSlug: "widgets",
			matches:  nil,
			want:     "",
		},
		{
			name:     "one match with base slug",
			baseSlug: "widgets",
			matches: []ProjectMatch{
				{Slug: "widgets"},
			},
			want: "widgets-1",
		},
		{
			name:     "two matches with serial",
			baseSlug: "widgets",
			matches: []ProjectMatch{
				{Slug: "widgets"},
				{Slug: "widgets-1"},
			},
			want: "widgets-2",
		},
		{
			name:     "gap in serial",
			baseSlug: "widgets",
			matches: []ProjectMatch{
				{Slug: "widgets"},
				{Slug: "widgets-3"},
			},
			want: "widgets-4",
		},
		{
			name:     "no base slug match but serial exists",
			baseSlug: "widgets",
			matches: []ProjectMatch{
				{Slug: "widgets-2"},
			},
			want: "widgets-3",
		},
		{
			name:     "unrelated slugs only",
			baseSlug: "widgets",
			matches: []ProjectMatch{
				{Slug: "gadgets"},
			},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NextSlugFromMatches(tt.baseSlug, tt.matches)
			if got != tt.want {
				t.Errorf("NextSlugFromMatches(%q, ...) = %q, want %q", tt.baseSlug, got, tt.want)
			}
		})
	}
}

func TestShowMatchingProjectsPrompt_AutoConfirm(t *testing.T) {
	matches := []ProjectMatch{
		{ID: "id-1", Name: "widgets", Slug: "widgets"},
		{ID: "id-2", Name: "widgets (2)", Slug: "widgets-2"},
	}

	choice, selectedID := ShowMatchingProjectsPrompt("widgets", matches, "widgets-3", true)
	if choice != ProjectChoiceLink {
		t.Errorf("expected ProjectChoiceLink, got %v", choice)
	}
	if selectedID != "id-1" {
		t.Errorf("expected selected ID 'id-1', got %q", selectedID)
	}
}
