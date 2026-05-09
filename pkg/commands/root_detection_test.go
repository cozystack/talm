// Copyright Cozystack Authors
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

// Contract: argument parsing for root-detection helpers in
// root_detection.go. These functions decide which directory becomes
// the talm project root based on user-supplied -f / --file / -t /
// --template flags. The contract is user-facing: the CLI behaviour
// users rely on when running `talm apply -f nodes/cp1.yaml,nodes/cp2.yaml`
// or `talm template --template templates/cluster.yaml`.

package commands

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

// === parseCommaSeparatedValues ===

// Contract: comma-separated values are split, trimmed, and empty
// entries are dropped. A single value (no comma) returns a one-element
// slice. Empty input returns nil.
func TestContract_ParseCommaSeparatedValues(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  []string
	}{
		{"empty", "", nil},
		{"single", "foo", []string{"foo"}},
		{"two values", "foo,bar", []string{"foo", "bar"}},
		{"three with whitespace", " foo , bar , baz ", []string{"foo", "bar", "baz"}},
		{"empty entries dropped", "foo,,bar,", []string{"foo", "bar"}},
		{"only commas", ",,,", nil},
		{"only whitespace", "   ", nil},
		{"path-like values", "nodes/cp1.yaml,nodes/cp2.yaml", []string{"nodes/cp1.yaml", "nodes/cp2.yaml"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseCommaSeparatedValues(tc.input)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("parseCommaSeparatedValues(%q) = %v, want %v", tc.input, got, tc.want)
			}
		})
	}
}

// === parseFlagFromArgs ===

// Contract: scans an argv-style slice for a flag in either short or
// long form, supports both space-separated (-f value) and equal-sign
// (-f=value) forms, and propagates comma-separated values. The first
// occurrence wins (later flag instances are ignored — this matches
// cobra's "first occurrence" semantics for non-slice flags). Stops
// at the first hit; does not scan further. Returns nil if the flag is
// absent or has no following value.
func TestContract_ParseFlagFromArgs(t *testing.T) {
	cases := []struct {
		name      string
		args      []string
		shortFlag string
		longFlag  string
		want      []string
	}{
		{
			name:      "short-form space-separated",
			args:      []string{"-f", "nodes/cp1.yaml", "--other"},
			shortFlag: "-f", longFlag: "--file",
			want: []string{"nodes/cp1.yaml"},
		},
		{
			name:      "long-form space-separated",
			args:      []string{"--file", "nodes/cp1.yaml"},
			shortFlag: "-f", longFlag: "--file",
			want: []string{"nodes/cp1.yaml"},
		},
		{
			name:      "short-form equal-sign",
			args:      []string{"-f=nodes/cp1.yaml"},
			shortFlag: "-f", longFlag: "--file",
			want: []string{"nodes/cp1.yaml"},
		},
		{
			name:      "long-form equal-sign",
			args:      []string{"--file=nodes/cp1.yaml"},
			shortFlag: "-f", longFlag: "--file",
			want: []string{"nodes/cp1.yaml"},
		},
		{
			name:      "comma-separated values via short form",
			args:      []string{"-f", "a.yaml,b.yaml,c.yaml"},
			shortFlag: "-f", longFlag: "--file",
			want: []string{"a.yaml", "b.yaml", "c.yaml"},
		},
		{
			name:      "absent flag",
			args:      []string{"--other", "x"},
			shortFlag: "-f", longFlag: "--file",
			want: nil,
		},
		{
			name:      "flag with no following value",
			args:      []string{"-f"},
			shortFlag: "-f", longFlag: "--file",
			want: nil,
		},
		{
			name:      "flag followed by another flag (no value)",
			args:      []string{"-f", "--other"},
			shortFlag: "-f", longFlag: "--file",
			want: nil,
		},
		{
			name:      "first occurrence wins",
			args:      []string{"-f", "first.yaml", "-f", "second.yaml"},
			shortFlag: "-f", longFlag: "--file",
			want: []string{"first.yaml"},
		},
		{
			name:      "templates flag (-t / --template)",
			args:      []string{"-t", "templates/cluster.yaml"},
			shortFlag: "-t", longFlag: "--template",
			want: []string{"templates/cluster.yaml"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseFlagFromArgs(tc.args, tc.shortFlag, tc.longFlag)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("parseFlagFromArgs(%v, %q, %q) = %v, want %v",
					tc.args, tc.shortFlag, tc.longFlag, got, tc.want)
			}
		})
	}
}

// === ResolveSecretsPath ===

// Contract: when the input is empty, the default file name is
// "secrets.yaml". Relative paths are anchored against Config.RootDir;
// absolute paths are returned as-is. The function uses filepath.Join,
// so the OS-native separator lands in the result — tests build
// expected values via filepath.Join so they match on Windows too.
func TestContract_ResolveSecretsPath(t *testing.T) {
	originalRoot := Config.RootDir
	t.Cleanup(func() { Config.RootDir = originalRoot })
	root := crossPlatformAbs("some", "project")
	Config.RootDir = root

	absSecrets := crossPlatformAbs("etc", "secrets.yaml")
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"empty defaults to secrets.yaml under root", "", filepath.Join(root, "secrets.yaml")},
		{"relative anchored to root", filepath.Join("vault", "secrets.yaml"), filepath.Join(root, "vault", "secrets.yaml")},
		{"absolute returned verbatim", absSecrets, absSecrets},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ResolveSecretsPath(tc.input)
			if got != tc.want {
				t.Errorf("ResolveSecretsPath(%q) = %q, want %q (Config.RootDir=%q)", tc.input, got, tc.want, Config.RootDir)
			}
		})
	}
}

// === ExpandFilePaths / findYAMLFiles ===

// Contract: ExpandFilePaths converts user-supplied paths to absolute
// paths. Files are returned as-is (caller handles non-existence
// later). Directories are expanded to all .yaml/.yml files inside,
// recursively. An empty directory is an error: the operator clearly
// asked for SOMETHING under that directory.
func TestContract_ExpandFilePaths_Files(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "node.yaml")
	if err := os.WriteFile(file, []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ExpandFilePaths([]string{file})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0] != file {
		t.Errorf("expected [%q], got %v", file, got)
	}
}

func TestContract_ExpandFilePaths_DirectoryRecursive(t *testing.T) {
	dir := t.TempDir()
	subdir := filepath.Join(dir, "sub")
	if err := os.Mkdir(subdir, 0o755); err != nil {
		t.Fatal(err)
	}
	yamlFiles := []string{
		filepath.Join(dir, "a.yaml"),
		filepath.Join(dir, "b.yml"),
		filepath.Join(subdir, "c.yaml"),
	}
	for _, f := range yamlFiles {
		if err := os.WriteFile(f, []byte("ok"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Non-YAML file: must be ignored.
	if err := os.WriteFile(filepath.Join(dir, "readme.md"), []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := ExpandFilePaths([]string{dir})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sort.Strings(got)
	want := append([]string{}, yamlFiles...)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("expanded YAML files mismatch\n got: %v\nwant: %v", got, want)
	}
}

// Contract: an empty directory under a `-f` argument is an explicit
// error. The operator pointed at "this directory holds my node
// configs"; an empty directory is a typo or wrong path, not "no
// nodes". Talm refuses silently rendering nothing.
func TestContract_ExpandFilePaths_EmptyDirectoryIsError(t *testing.T) {
	dir := t.TempDir()
	_, err := ExpandFilePaths([]string{dir})
	if err == nil {
		t.Fatal("expected error for empty directory, got nil")
	}
}

// Contract: a non-existent path is NOT an error at expansion time —
// it is returned as-is (with absolute resolution) so the caller can
// produce a precise downstream error. ExpandFilePaths is just glob
// expansion, not validation.
func TestContract_ExpandFilePaths_NonExistentPathPropagated(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist.yaml")
	got, err := ExpandFilePaths([]string{missing})
	if err != nil {
		t.Fatalf("expected no error for missing file (caller validates), got: %v", err)
	}
	if len(got) != 1 || got[0] != missing {
		t.Errorf("expected [%q], got %v", missing, got)
	}
}

// === isValidPreset ===

// Contract: preset name must appear verbatim in the available list.
// Case-sensitive; no fuzzy matching. The list comes from
// pkg/generated/presets.go (built at chart-bake time).
func TestContract_IsValidPreset(t *testing.T) {
	available := []string{presetCozystack, presetGeneric, "talos"}

	for _, name := range available {
		if !isValidPreset(name, available) {
			t.Errorf("isValidPreset(%q, %v) = false, want true", name, available)
		}
	}
	for _, name := range []string{"Cozystack", "COZYSTACK", "unknown", "", " cozystack"} {
		if isValidPreset(name, available) {
			t.Errorf("isValidPreset(%q, %v) = true, want false", name, available)
		}
	}
}

// === fileExists ===

// Contract: fileExists is a thin wrapper around os.Stat reporting
// only existence (true/false). Permission errors and I/O failures
// register as "does not exist" — the chart-side flow that calls this
// (printSecretsWarning, kubeconfig handling) treats both states
// identically anyway.
func TestContract_FileExists(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "f")
	if err := os.WriteFile(existing, []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !fileExists(existing) {
		t.Errorf("fileExists(%q) = false, want true", existing)
	}
	if fileExists(filepath.Join(dir, "missing")) {
		t.Errorf("fileExists for missing file returned true")
	}
}

// === filesDiffer ===

// Contract: filesDiffer compares an existing file's bytes against a
// proposed new content. Returns:
//   - (true, nil) if file does not exist (any new content "differs"
//     from a non-existent file from the operator-intent perspective)
//   - (true, nil) if contents differ
//   - (false, nil) if contents match exactly (used to skip a noisy
//     "do you want to overwrite?" prompt when the new content equals
//     what's already on disk)
//   - (false, err) on read errors other than not-exist
func TestContract_FilesDiffer(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "f")

	// Missing file → differs.
	differ, err := filesDiffer(existing, []byte("hello"))
	if err != nil {
		t.Fatalf("unexpected error for missing file: %v", err)
	}
	if !differ {
		t.Errorf("expected differs=true for missing file")
	}

	// Equal content → does not differ.
	if err := os.WriteFile(existing, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	differ, err = filesDiffer(existing, []byte("hello"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if differ {
		t.Errorf("expected differs=false for equal content")
	}

	// Different content → differs.
	differ, err = filesDiffer(existing, []byte("world"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !differ {
		t.Errorf("expected differs=true for changed content")
	}
}
