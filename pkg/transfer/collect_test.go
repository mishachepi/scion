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

package transfer

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewManifestBuilder(t *testing.T) {
	builder := NewManifestBuilder("/tmp/test")

	if builder.BasePath != "/tmp/test" {
		t.Errorf("unexpected BasePath: %s", builder.BasePath)
	}

	// Check default exclude patterns
	if len(builder.ExcludePatterns) != len(DefaultExcludePatterns) {
		t.Errorf("expected %d default exclude patterns, got %d",
			len(DefaultExcludePatterns), len(builder.ExcludePatterns))
	}
}

func TestManifestBuilder_WithExcludePatterns(t *testing.T) {
	builder := NewManifestBuilder("/tmp/test").
		WithExcludePatterns([]string{"*.log", "node_modules/**"})

	expectedCount := len(DefaultExcludePatterns) + 2
	if len(builder.ExcludePatterns) != expectedCount {
		t.Errorf("expected %d patterns, got %d", expectedCount, len(builder.ExcludePatterns))
	}
}

func TestManifestBuilder_Build(t *testing.T) {
	// Create a temporary directory structure
	dir := t.TempDir()

	// Create test files
	files := map[string]string{
		"file1.txt":     "content1",
		"dir/file2.txt": "content2",
		"dir/file3.txt": "content3",
	}

	for path, content := range files {
		fullPath := filepath.Join(dir, path)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
			t.Fatalf("failed to create directory: %v", err)
		}
		if err := os.WriteFile(fullPath, []byte(content), 0644); err != nil {
			t.Fatalf("failed to create file: %v", err)
		}
	}

	builder := NewManifestBuilder(dir)
	manifest, err := builder.Build()
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	if manifest.Version != "1.0" {
		t.Errorf("unexpected version: %s", manifest.Version)
	}

	if len(manifest.Files) != len(files) {
		t.Errorf("expected %d files, got %d", len(files), len(manifest.Files))
	}

	// Verify content hash is computed
	if manifest.ContentHash == "" {
		t.Error("expected ContentHash to be set")
	}

	// Verify files are sorted
	for i := 1; i < len(manifest.Files); i++ {
		if manifest.Files[i].Path < manifest.Files[i-1].Path {
			t.Error("files should be sorted by path")
		}
	}
}

func TestManifestBuilder_ExcludesGit(t *testing.T) {
	dir := t.TempDir()

	// Create .git directory with files
	gitDir := filepath.Join(dir, ".git")
	if err := os.MkdirAll(gitDir, 0755); err != nil {
		t.Fatalf("failed to create .git: %v", err)
	}
	if err := os.WriteFile(filepath.Join(gitDir, "config"), []byte("config"), 0644); err != nil {
		t.Fatalf("failed to create .git/config: %v", err)
	}

	// Create regular file
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main"), 0644); err != nil {
		t.Fatalf("failed to create main.go: %v", err)
	}

	builder := NewManifestBuilder(dir)
	manifest, err := builder.Build()
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	// Should only have main.go
	if len(manifest.Files) != 1 {
		t.Errorf("expected 1 file, got %d", len(manifest.Files))
	}

	if manifest.Files[0].Path != "main.go" {
		t.Errorf("unexpected file: %s", manifest.Files[0].Path)
	}
}

func TestManifestBuilder_ExcludesDSStore(t *testing.T) {
	dir := t.TempDir()

	// Create .DS_Store files
	if err := os.WriteFile(filepath.Join(dir, ".DS_Store"), []byte("ds"), 0644); err != nil {
		t.Fatalf("failed to create .DS_Store: %v", err)
	}

	subdir := filepath.Join(dir, "subdir")
	if err := os.MkdirAll(subdir, 0755); err != nil {
		t.Fatalf("failed to create subdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(subdir, ".DS_Store"), []byte("ds"), 0644); err != nil {
		t.Fatalf("failed to create subdir/.DS_Store: %v", err)
	}

	// Create regular file
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main"), 0644); err != nil {
		t.Fatalf("failed to create main.go: %v", err)
	}

	builder := NewManifestBuilder(dir)
	manifest, err := builder.Build()
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	// Should only have main.go
	if len(manifest.Files) != 1 {
		t.Errorf("expected 1 file, got %d", len(manifest.Files))
	}

	for _, f := range manifest.Files {
		if strings.Contains(f.Path, ".DS_Store") {
			t.Errorf("should not include .DS_Store: %s", f.Path)
		}
	}
}

func TestCollectFiles(t *testing.T) {
	dir := t.TempDir()

	// Create test files
	if err := os.WriteFile(filepath.Join(dir, "file1.txt"), []byte("content1"), 0644); err != nil {
		t.Fatalf("failed to create file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "file2.txt"), []byte("content2"), 0755); err != nil {
		t.Fatalf("failed to create file: %v", err)
	}

	files, err := CollectFiles(dir, nil)
	if err != nil {
		t.Fatalf("CollectFiles failed: %v", err)
	}

	if len(files) != 2 {
		t.Errorf("expected 2 files, got %d", len(files))
	}

	// Verify FullPath is set
	for _, f := range files {
		if f.FullPath == "" {
			t.Error("expected FullPath to be set")
		}
		if !filepath.IsAbs(f.FullPath) {
			t.Errorf("FullPath should be absolute: %s", f.FullPath)
		}
	}
}

func TestCollectFiles_WithExcludePatterns(t *testing.T) {
	dir := t.TempDir()

	// Create test files
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("code"), 0644); err != nil {
		t.Fatalf("failed to create file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "debug.log"), []byte("logs"), 0644); err != nil {
		t.Fatalf("failed to create file: %v", err)
	}

	files, err := CollectFiles(dir, []string{"*.log"})
	if err != nil {
		t.Fatalf("CollectFiles failed: %v", err)
	}

	if len(files) != 1 {
		t.Errorf("expected 1 file (excluding .log), got %d", len(files))
	}

	if files[0].Path != "main.go" {
		t.Errorf("unexpected file: %s", files[0].Path)
	}
}

func TestBuildManifest(t *testing.T) {
	files := []FileInfo{
		{Path: "b.txt", Size: 100, Hash: "sha256:bbb"},
		{Path: "a.txt", Size: 50, Hash: "sha256:aaa"},
	}

	manifest := BuildManifest(files)

	if manifest.Version != "1.0" {
		t.Errorf("unexpected version: %s", manifest.Version)
	}

	if manifest.ContentHash == "" {
		t.Error("expected ContentHash to be set")
	}

	if len(manifest.Files) != 2 {
		t.Errorf("expected 2 files, got %d", len(manifest.Files))
	}
}

func TestManifestBuilder_IncludesDotfiles(t *testing.T) {
	dir := t.TempDir()

	// Create a home/.tmux.conf dotfile
	homeDir := filepath.Join(dir, "home")
	if err := os.MkdirAll(homeDir, 0755); err != nil {
		t.Fatalf("failed to create home dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(homeDir, ".tmux.conf"), []byte("set -g mouse on"), 0644); err != nil {
		t.Fatalf("failed to create .tmux.conf: %v", err)
	}

	// Create a regular file too
	if err := os.WriteFile(filepath.Join(dir, "scion-agent.yaml"), []byte("name: test"), 0644); err != nil {
		t.Fatalf("failed to create scion-agent.yaml: %v", err)
	}

	builder := NewManifestBuilder(dir)
	manifest, err := builder.Build()
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	// Should include both files
	if len(manifest.Files) != 2 {
		t.Errorf("expected 2 files, got %d", len(manifest.Files))
	}

	// Verify home/.tmux.conf is in the manifest with the correct relative path
	found := false
	for _, f := range manifest.Files {
		if f.Path == "home/.tmux.conf" {
			found = true
			break
		}
	}
	if !found {
		paths := make([]string, len(manifest.Files))
		for i, f := range manifest.Files {
			paths[i] = f.Path
		}
		t.Errorf("expected manifest to include home/.tmux.conf, got: %v", paths)
	}
}

func TestCollectFiles_SkipsSymlinks(t *testing.T) {
	dir := t.TempDir()

	// Create a regular file
	if err := os.WriteFile(filepath.Join(dir, "real.txt"), []byte("content"), 0644); err != nil {
		t.Fatalf("failed to create file: %v", err)
	}

	// Create a valid symlink
	validTarget := filepath.Join(dir, "real.txt")
	if err := os.Symlink(validTarget, filepath.Join(dir, "valid-link")); err != nil {
		t.Fatalf("failed to create valid symlink: %v", err)
	}

	// Create a dangling symlink (like .claude/debug/latest)
	if err := os.Symlink("/nonexistent/path", filepath.Join(dir, "dangling-link")); err != nil {
		t.Fatalf("failed to create dangling symlink: %v", err)
	}

	files, err := CollectFiles(dir, nil)
	if err != nil {
		t.Fatalf("CollectFiles failed: %v", err)
	}

	// Should only include the real file, not any symlinks
	if len(files) != 1 {
		names := make([]string, len(files))
		for i, f := range files {
			names[i] = f.Path
		}
		t.Errorf("expected 1 file (skipping symlinks), got %d: %v", len(files), names)
	}

	if files[0].Path != "real.txt" {
		t.Errorf("expected real.txt, got %s", files[0].Path)
	}
}

func TestBuildManifest_Empty(t *testing.T) {
	manifest := BuildManifest([]FileInfo{})

	if manifest.Version != "1.0" {
		t.Errorf("unexpected version: %s", manifest.Version)
	}

	if len(manifest.Files) != 0 {
		t.Errorf("expected 0 files, got %d", len(manifest.Files))
	}
}

// requireUnprivileged skips permission-based tests when the process can read
// anything regardless of mode, which makes them silently vacuous.
func requireUnprivileged(t *testing.T) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("running as root: permission bits do not restrict this process")
	}
}

// A directory the process cannot open must not take the whole walk down with
// it. Reproduces the macOS .Trashes failure (mode d-wx--x--t at a volume root)
// that made every `scion start` in such a project fail with
// "failed to collect workspace files: ... permission denied".
func TestManifestBuilder_CollectFiles_UnreadableDirDoesNotAbortWalk(t *testing.T) {
	requireUnprivileged(t)
	dir := t.TempDir()

	if err := os.WriteFile(filepath.Join(dir, "keep.txt"), []byte("keep"), 0o644); err != nil {
		t.Fatalf("write keep.txt: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "nested"), 0o755); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "nested", "deep.txt"), []byte("deep"), 0o644); err != nil {
		t.Fatalf("write deep.txt: %v", err)
	}

	// Write-and-traverse but not readable — exactly .Trashes' mode.
	locked := filepath.Join(dir, "locked")
	if err := os.MkdirAll(locked, 0o755); err != nil {
		t.Fatalf("mkdir locked: %v", err)
	}
	if err := os.WriteFile(filepath.Join(locked, "hidden.txt"), []byte("hidden"), 0o644); err != nil {
		t.Fatalf("write hidden.txt: %v", err)
	}
	if err := os.Chmod(locked, 0o311); err != nil {
		t.Fatalf("chmod locked: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })

	files, err := NewManifestBuilder(dir).CollectFiles()
	if err != nil {
		t.Fatalf("expected the walk to survive an unreadable directory, got: %v", err)
	}

	got := make(map[string]bool, len(files))
	for _, f := range files {
		got[f.Path] = true
	}
	for _, want := range []string{"keep.txt", "nested/deep.txt"} {
		if !got[want] {
			t.Errorf("expected %s to be collected, got %v", want, got)
		}
	}
	if got["locked/hidden.txt"] {
		t.Error("collected a file from an unreadable directory")
	}
}

// The base path is the one permission error that must stay fatal: an
// unreadable root means there is no workspace to collect, and reporting
// success with zero files would hide that from the caller.
func TestManifestBuilder_CollectFiles_UnreadableBasePathIsFatal(t *testing.T) {
	requireUnprivileged(t)
	parent := t.TempDir()
	base := filepath.Join(parent, "base")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatalf("mkdir base: %v", err)
	}
	if err := os.WriteFile(filepath.Join(base, "file.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write file.txt: %v", err)
	}
	if err := os.Chmod(base, 0o311); err != nil {
		t.Fatalf("chmod base: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(base, 0o755) })

	if _, err := NewManifestBuilder(base).CollectFiles(); err == nil {
		t.Fatal("expected an error when the base path itself is unreadable")
	}
}

// Only regular files are collectable. A unix socket is the case seen in the
// wild — live agent homes keep keyring/gpg-agent control sockets, and opening
// one to hash it fails with "operation not supported on socket", which used to
// abort the walk. A FIFO is not exercised here on purpose: hashing one would
// block rather than fail, so a regression would hang the suite instead of
// reporting; the same !IsRegular() branch covers both.
func TestManifestBuilder_CollectFiles_SkipsNonRegularFiles(t *testing.T) {
	// Short base path: unix socket paths are capped near 104 bytes, well below
	// what t.TempDir() produces under /var/folders on macOS.
	dir, err := os.MkdirTemp("/tmp", "sk")
	if err != nil {
		t.Fatalf("mkdtemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	if err := os.WriteFile(filepath.Join(dir, "keep.txt"), []byte("keep"), 0o644); err != nil {
		t.Fatalf("write keep.txt: %v", err)
	}

	sockPath := filepath.Join(dir, "control")
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Skipf("cannot create a unix socket here: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	files, err := NewManifestBuilder(dir).CollectFiles()
	if err != nil {
		t.Fatalf("expected the walk to skip a socket, got: %v", err)
	}

	if len(files) != 1 || files[0].Path != "keep.txt" {
		var names []string
		for _, f := range files {
			names = append(names, f.Path)
		}
		t.Errorf("expected only keep.txt, got %v", names)
	}
}

// macOS keeps bookkeeping directories at every volume root. They are never
// part of a workspace, so they must be excluded on their own merit rather
// than only surviving via the permission tolerance above.
func TestManifestBuilder_CollectFiles_ExcludesMacVolumeSystemDirs(t *testing.T) {
	dir := t.TempDir()

	if err := os.WriteFile(filepath.Join(dir, "keep.txt"), []byte("keep"), 0o644); err != nil {
		t.Fatalf("write keep.txt: %v", err)
	}
	for _, sysDir := range []string{".Trashes", ".fseventsd", ".Spotlight-V100", ".DocumentRevisions-V100", ".TemporaryItems"} {
		full := filepath.Join(dir, sysDir)
		if err := os.MkdirAll(full, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", sysDir, err)
		}
		if err := os.WriteFile(filepath.Join(full, "junk.txt"), []byte("junk"), 0o644); err != nil {
			t.Fatalf("write junk into %s: %v", sysDir, err)
		}
	}

	files, err := NewManifestBuilder(dir).CollectFiles()
	if err != nil {
		t.Fatalf("CollectFiles: %v", err)
	}

	if len(files) != 1 || files[0].Path != "keep.txt" {
		var names []string
		for _, f := range files {
			names = append(names, f.Path)
		}
		t.Errorf("expected only keep.txt, got %v", names)
	}
}
