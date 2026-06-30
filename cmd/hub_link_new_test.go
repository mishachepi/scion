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

package cmd

import (
	"strings"
	"testing"

	"github.com/GoogleCloudPlatform/scion/pkg/store"
)

func TestBuildLinkNewRequest_NameFromFlag(t *testing.T) {
	req, err := buildLinkNewRequest("Infra Main", "", "", nil, "fallback")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.Name != "Infra Main" {
		t.Errorf("Name = %q, want %q", req.Name, "Infra Main")
	}
	if req.Slug != "infra-main" {
		t.Errorf("Slug = %q, want slugify of name", req.Slug)
	}
}

func TestBuildLinkNewRequest_FallbackToBasename(t *testing.T) {
	req, err := buildLinkNewRequest("", "", "", nil, "infra-main")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.Name != "infra-main" {
		t.Errorf("Name = %q, want fallback basename", req.Name)
	}
	if req.Slug != "infra-main" {
		t.Errorf("Slug = %q, want %q", req.Slug, "infra-main")
	}
}

func TestBuildLinkNewRequest_ExplicitSlugWins(t *testing.T) {
	req, err := buildLinkNewRequest("Infra Main", "custom-slug", "", nil, "fb")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.Slug != "custom-slug" {
		t.Errorf("Slug = %q, want %q", req.Slug, "custom-slug")
	}
}

func TestBuildLinkNewRequest_EmptyNameAndFallback(t *testing.T) {
	for _, bad := range []string{"", ".", "/"} {
		_, err := buildLinkNewRequest("", "", "", nil, bad)
		if err == nil {
			t.Errorf("fallback %q: expected error, got nil", bad)
		}
	}
}

func TestBuildLinkNewRequest_WorkspaceModeSetsLabel(t *testing.T) {
	for _, mode := range []string{
		store.WorkspaceModeShared,
		store.WorkspaceModePerAgent,
		store.WorkspaceModeWorktreePerAgent,
		store.WorkspaceModeInPlace,
	} {
		req, err := buildLinkNewRequest("X", "", mode, nil, "")
		if err != nil {
			t.Fatalf("mode %q: unexpected error: %v", mode, err)
		}
		if got := req.Labels[store.LabelWorkspaceMode]; got != mode {
			t.Errorf("mode %q: label = %q, want %q", mode, got, mode)
		}
	}
}

func TestBuildLinkNewRequest_InvalidWorkspaceModeRejected(t *testing.T) {
	_, err := buildLinkNewRequest("X", "", "exotic", nil, "")
	if err == nil {
		t.Fatal("expected error for invalid workspace-mode")
	}
	if !strings.Contains(err.Error(), "invalid --workspace-mode") {
		t.Errorf("error %q does not mention workspace-mode", err)
	}
}

func TestBuildLinkNewRequest_LabelsParsed(t *testing.T) {
	req, err := buildLinkNewRequest("X", "", "", []string{"a=1", "owner=mikhail"}, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.Labels["a"] != "1" || req.Labels["owner"] != "mikhail" {
		t.Errorf("labels = %#v, missing entries", req.Labels)
	}
}

func TestBuildLinkNewRequest_LabelsAndWorkspaceModeCoexist(t *testing.T) {
	req, err := buildLinkNewRequest("X", "", store.WorkspaceModeInPlace,
		[]string{"team=ops"}, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.Labels["team"] != "ops" {
		t.Errorf("team label dropped: %#v", req.Labels)
	}
	if req.Labels[store.LabelWorkspaceMode] != store.WorkspaceModeInPlace {
		t.Errorf("workspace-mode label missing: %#v", req.Labels)
	}
}

func TestBuildLinkNewRequest_MalformedLabelRejected(t *testing.T) {
	cases := []string{"novalue", "=value", "  =1"}
	for _, c := range cases {
		_, err := buildLinkNewRequest("X", "", "", []string{c}, "")
		if err == nil {
			t.Errorf("label %q: expected error, got nil", c)
		}
	}
}
