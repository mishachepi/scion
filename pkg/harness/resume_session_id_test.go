// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package harness

import (
	"strings"
	"testing"

	"github.com/GoogleCloudPlatform/scion/pkg/config"
)

// Resume-by-session-id behavior across all GetCommand impls.
// Captures the "in-place workspace mode" fix: --continue picks the most
// recent jsonl in a shared cwd and silently cross-talks between agents;
// --resume <id> pins exactly. See scion-mch/10-harness-session-id-pinning.md.

func TestDeclarativeGenericHarness_GetCommand_ResumeByID(t *testing.T) {
	entry := config.HarnessConfigEntry{
		Harness: "fakeharness",
		Command: &config.HarnessCommandConfig{
			Base:         []string{"fake"},
			ResumeFlag:   "--continue",
			ResumeIDFlag: "--resume {session_id}",
			TaskPosition: "after_base_args",
		},
	}
	h := NewDeclarativeGenericHarness(entry)

	// resume=true + id → resume_id_flag wins (with placeholder substituted).
	got := h.GetCommand("", true, "uuid-1", nil)
	want := []string{"fake", "--resume", "uuid-1"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("resume with id: got %v want %v", got, want)
	}

	// resume=true + empty id → fallback to resume_flag (--continue).
	got = h.GetCommand("", true, "", nil)
	want = []string{"fake", "--continue"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("resume without id: got %v want %v", got, want)
	}

	// resume=false → no resume flag regardless of id.
	got = h.GetCommand("", false, "uuid-1", nil)
	want = []string{"fake"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("no resume: got %v want %v", got, want)
	}
}

func TestDeclarativeGenericHarness_GetCommand_ResumeIDFlag_OnlyResumeFlagSet(t *testing.T) {
	// When resume_id_flag is empty, even a known id must fall back to
	// resume_flag — we don't want to silently drop the resume request.
	entry := config.HarnessConfigEntry{
		Harness: "fakeharness",
		Command: &config.HarnessCommandConfig{
			Base:       []string{"fake"},
			ResumeFlag: "--continue",
		},
	}
	h := NewDeclarativeGenericHarness(entry)

	got := h.GetCommand("", true, "uuid-1", nil)
	want := []string{"fake", "--continue"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("no resume_id_flag: got %v want %v", got, want)
	}
}

func TestContainerScriptHarness_GetCommand_ResumeByID(t *testing.T) {
	h, _ := newTestContainerScriptHarness(t)
	// newTestContainerScriptHarness sets ResumeFlag="--resume" but no
	// ResumeIDFlag; with an id present and no template, must stay on
	// the bare resume flag (regression check).
	got := h.GetCommand("", true, "uuid-1", nil)
	want := []string{"testcli", "--resume"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("resume id with no template: got %v want %v", got, want)
	}
}

func TestBuildResumeTokens_PlaceholderSubstitution(t *testing.T) {
	cmd := &config.HarnessCommandConfig{
		ResumeFlag:   "--continue",
		ResumeIDFlag: "--resume {session_id}",
	}
	got := buildResumeTokens(cmd, "deadbeef")
	want := []string{"--resume", "deadbeef"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("placeholder: got %v want %v", got, want)
	}

	// Empty cmd → nil.
	if buildResumeTokens(nil, "x") != nil {
		t.Error("nil cmd must return nil tokens")
	}

	// Empty id falls back to resume_flag.
	got = buildResumeTokens(cmd, "")
	want = []string{"--continue"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("empty id fallback: got %v want %v", got, want)
	}

	// Both empty → nil.
	if buildResumeTokens(&config.HarnessCommandConfig{}, "") != nil {
		t.Error("both empty must return nil")
	}
}
