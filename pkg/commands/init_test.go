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

package commands

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cockroachdb/errors"
	"gopkg.in/yaml.v3"
)

// writeToDestinationSilentOnFailureCases asserts neither writeTo-
// Destination nor writeSecureToDestination emits a misleading
// "Created <path>" line when the underlying write fails. The failure
// is induced by pointing destination at an existing directory, which
// makes os.WriteFile (and therefore secureperm.WriteFile) fail.
func TestWriteToDestination_SilentOnFailure(t *testing.T) {
	forceOrig := initCmdFlags.force
	sinkOrig := createdSink
	t.Cleanup(func() {
		initCmdFlags.force = forceOrig
		createdSink = sinkOrig
	})

	initCmdFlags.force = true

	cases := []struct {
		name string
		call func(data []byte, dest string) error
	}{
		{
			name: "writeToDestination",
			call: func(data []byte, dest string) error {
				return writeToDestination(data, dest, 0o644)
			},
		},
		{
			name: "writeSecureToDestination",
			call: func(data []byte, dest string) error {
				return writeSecureToDestination(data, dest)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			createdSink = &buf

			// Destination is an existing directory — the underlying write
			// call fails without touching the filesystem content.
			dir := t.TempDir()

			err := tc.call([]byte("data"), dir)
			if err == nil {
				t.Fatal("expected error writing to directory, got nil")
			}
			if strings.Contains(buf.String(), "Created") {
				t.Errorf("output contains 'Created' despite failure: %q", buf.String())
			}
		})
	}
}

// TestWriteToDestination_AnnouncesOnSuccess pins that the happy path
// still prints "Created <path>" — i.e. the new err==nil guard didn't
// accidentally silence the normal success message.
func TestWriteToDestination_AnnouncesOnSuccess(t *testing.T) {
	forceOrig := initCmdFlags.force
	sinkOrig := createdSink
	t.Cleanup(func() {
		initCmdFlags.force = forceOrig
		createdSink = sinkOrig
	})

	initCmdFlags.force = true

	var buf bytes.Buffer
	createdSink = &buf

	dir := t.TempDir()
	path := filepath.Join(dir, "ok.txt")

	if err := writeToDestination([]byte("x"), path, 0o644); err != nil {
		t.Fatalf("writeToDestination: %v", err)
	}
	if !strings.Contains(buf.String(), "Created "+path) {
		t.Errorf("expected 'Created %s' in output, got %q", path, buf.String())
	}
}

// compile-time assert createdSink is writer-shaped.
var _ io.Writer = (*bytes.Buffer)(nil)

// TestApplyImageOverride pins the contract of the values.yaml image
// substitution that backs `talm init --image <ref>`: an empty override
// is a no-op, a non-empty override replaces the top-level `image:`
// line with the user's reference and preserves surrounding content,
// and a values.yaml without an `image:` field returns an error so the
// caller can surface the flag/preset mismatch instead of silently
// dropping --image.
func TestApplyImageOverride(t *testing.T) {
	original := []byte(`# Cluster endpoint.
endpoint: ""

floatingIP: ""

# Optional override for the link Layer2VIPConfig is pinned to.
vipLink: ""

image: "ghcr.io/cozystack/cozystack/talos:v1.12.6"
podSubnets:
- 10.244.0.0/16
`)

	t.Run("empty override returns input unchanged", func(t *testing.T) {
		got, err := applyImageOverride(original, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !bytes.Equal(got, original) {
			t.Errorf("empty override mutated the input:\n%s", got)
		}
	})

	t.Run("non-empty override replaces the image line", func(t *testing.T) {
		got, err := applyImageOverride(original, "factory.talos.dev/installer/abc:v1.13.0")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := `image: "factory.talos.dev/installer/abc:v1.13.0"`
		if !bytes.Contains(got, []byte(want)) {
			t.Errorf("expected %q in output, got:\n%s", want, got)
		}
		if bytes.Contains(got, []byte("ghcr.io/cozystack/cozystack/talos:v1.12.6")) {
			t.Errorf("original image still present after override:\n%s", got)
		}
		for _, marker := range []string{
			"# Cluster endpoint.",
			"floatingIP: \"\"",
			"vipLink: \"\"",
			"podSubnets:",
			"- 10.244.0.0/16",
		} {
			if !bytes.Contains(got, []byte(marker)) {
				t.Errorf("override stripped surrounding content %q:\n%s", marker, got)
			}
		}
	})

	t.Run("values without image field returns an error", func(t *testing.T) {
		noImage := []byte("endpoint: \"https://10.0.0.1:6443\"\nfloatingIP: \"\"\n")
		_, err := applyImageOverride(noImage, "factory.talos.dev/installer/abc:v1.13.0")
		if err == nil {
			t.Fatal("expected an error when --image is set but values.yaml has no image: field; silent no-op would lose the user's flag")
		}
		if !strings.Contains(err.Error(), "image:") {
			t.Errorf("error should name the missing field so the user knows what to do, got: %v", err)
		}
	})

	t.Run("rejects image: without a space after the colon", func(t *testing.T) {
		// image:noSpace and image:foo are not valid YAML
		// (yaml.v3 rejects key:value with no space). The regex
		// must NOT match these, so a broken preset surfaces as a
		// "no image: field" error from validateImageOverride
		// rather than silently getting rewritten.
		broken := []string{
			"image:noSpace\n",
			"image:ghcr.io/foo:v1\n",
		}
		for _, input := range broken {
			_, err := applyImageOverride([]byte(input), "factory.talos.dev/installer/abc:v1.13.0")
			if err == nil {
				t.Errorf("expected an error on broken YAML input %q (no space after colon), got none", input)
			}
		}
	})

	t.Run("matches image: with bare colon (empty value, valid YAML)", func(t *testing.T) {
		// `image:` followed by end-of-line is a YAML key with
		// nil value — valid YAML. The regex MUST still match
		// so --image can populate the empty preset slot.
		got, err := applyImageOverride([]byte("image:\n"), "factory.talos.dev/installer/abc:v1.13.0")
		if err != nil {
			t.Fatalf("expected the empty-value form to be rewritable, got: %v", err)
		}

		if !bytes.Contains(got, []byte(`image: "factory.talos.dev/installer/abc:v1.13.0"`)) {
			t.Errorf("empty-value form should be rewritten by --image, got:\n%s", got)
		}
	})

	t.Run("supports unquoted, single-quoted, and trailing-comment styles", func(t *testing.T) {
		styles := []struct {
			name string
			in   string
		}{
			{"double-quoted", `image: "ghcr.io/foo:v1"` + "\n"},
			{"single-quoted", `image: 'ghcr.io/foo:v1'` + "\n"},
			{"unquoted", "image: ghcr.io/foo:v1\n"},
			{"trailing-comment", `image: "ghcr.io/foo:v1" # default` + "\n"},
		}
		for _, s := range styles {
			t.Run(s.name, func(t *testing.T) {
				got, err := applyImageOverride([]byte(s.in), "factory.talos.dev/installer/abc:v1.13.0")
				if err != nil {
					t.Fatalf("unexpected error on %s style: %v", s.name, err)
				}
				if bytes.Contains(got, []byte("ghcr.io/foo:v1")) {
					t.Errorf("original image survived on %s style:\n%s", s.name, got)
				}
				if !bytes.Contains(got, []byte(`image: "factory.talos.dev/installer/abc:v1.13.0"`)) {
					t.Errorf("override missing on %s style:\n%s", s.name, got)
				}
			})
		}
	})

	t.Run("override containing dollar sequences round-trips verbatim", func(t *testing.T) {
		// regexp.ReplaceAll expands $0 / $1 / $name / ${name} in the
		// replacement, so a naive helper would silently corrupt an
		// image reference like factory.talos.dev/$tenant/foo:v1 (the
		// $tenant segment would resolve to an empty backreference and
		// disappear). The helper goes through ReplaceAllFunc instead
		// so the override bytes are written verbatim. Pin every
		// expansion form that regexp.Expand recognizes so a future
		// switch back to ReplaceAll is caught here.
		dollarCases := []string{
			`factory.talos.dev/$tenant/foo:v1`,
			`factory.talos.dev/$0/foo:v1`,
			`factory.talos.dev/${name}/foo:v1`,
			`factory.talos.dev/installer$1foo:v1`,
		}
		for _, override := range dollarCases {
			t.Run(override, func(t *testing.T) {
				got, err := applyImageOverride(original, override)
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				var parsed struct {
					Image string `yaml:"image"`
				}
				if err := yaml.Unmarshal(got, &parsed); err != nil {
					t.Fatalf("yaml.Unmarshal failed on helper output: %v\n%s", err, got)
				}
				if parsed.Image != override {
					t.Errorf("override with $-sequence round-trip mismatch: got image=%q, want %q\nhelper output:\n%s", parsed.Image, override, got)
				}
			})
		}
	})

	t.Run("output round-trips through yaml.Unmarshal", func(t *testing.T) {
		// Pin that the override produces a YAML string the parser
		// reads back as the original input — no escape-sequence
		// surprises from %q quoting.
		got, err := applyImageOverride(original, "factory.talos.dev/installer/abc:v1.13.0")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		var parsed struct {
			Image string `yaml:"image"`
		}
		if err := yaml.Unmarshal(got, &parsed); err != nil {
			t.Fatalf("yaml.Unmarshal failed on helper output: %v\n%s", err, got)
		}
		if parsed.Image != "factory.talos.dev/installer/abc:v1.13.0" {
			t.Errorf("round-trip mismatch: got image=%q, want %q", parsed.Image, "factory.talos.dev/installer/abc:v1.13.0")
		}
	})
}

// TestInitPreRunRejectsImageWithExclusiveModes pins the up-front
// rejection of --image when combined with --encrypt, --decrypt, or
// --update. Without it, the flag silently no-ops on those paths
// (they early-return before the preset write loop runs) and the user
// has no signal that their intent was discarded.
func TestInitPreRunRejectsImageWithExclusiveModes(t *testing.T) {
	imageOrig := initCmdFlags.image
	encryptOrig := initCmdFlags.encrypt
	decryptOrig := initCmdFlags.decrypt
	updateOrig := initCmdFlags.update
	t.Cleanup(func() {
		initCmdFlags.image = imageOrig
		initCmdFlags.encrypt = encryptOrig
		initCmdFlags.decrypt = decryptOrig
		initCmdFlags.update = updateOrig
	})

	cases := []struct {
		name string
		set  func()
	}{
		{"encrypt", func() { initCmdFlags.encrypt = true }},
		{"decrypt", func() { initCmdFlags.decrypt = true }},
		{"update", func() { initCmdFlags.update = true }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			initCmdFlags.image = "factory.talos.dev/installer/abc:v1.13.0"
			initCmdFlags.encrypt = false
			initCmdFlags.decrypt = false
			initCmdFlags.update = false
			tc.set()

			err := initCmd.PreRunE(initCmd, nil)
			if err == nil {
				t.Fatalf("expected --image with --%s to error in PreRunE", tc.name)
			}
			if !strings.Contains(err.Error(), "--image") {
				t.Errorf("error must name --image so the user can act on it, got: %v", err)
			}
		})
	}
}

// TestUpdateTalmLibraryChartRejectsImageFlag pins that --image is
// honored on initial init only. Letting it slip through --update
// would silently no-op (the update path does not touch the project's
// existing values.yaml content), losing the user's flag — same UX
// trap the validation in init proper exists to prevent.
func TestUpdateTalmLibraryChartRejectsImageFlag(t *testing.T) {
	imageOrig := initCmdFlags.image
	t.Cleanup(func() { initCmdFlags.image = imageOrig })

	initCmdFlags.image = "factory.talos.dev/installer/abc:v1.13.0"
	err := updateTalmLibraryChart()
	if err == nil {
		t.Fatal("expected --update to reject --image; got nil error")
	}
	if !strings.Contains(err.Error(), "--image") {
		t.Errorf("error must name --image so the user can act on it, got: %v", err)
	}
}

// TestInitUpdate_PresetNotFoundError_SingleWrapped pins that the
// "preset not declared anywhere" error path for `talm init --update`
// surfaces once, not twice. readChartYamlPreset already returns a
// cockroachdb/errors.WithHint("preset not found in Chart.yaml
// dependencies", "add a preset chart…"); wrapping that again with
// errors.Wrap(err, "preset is required: use --preset flag or ensure
// Chart.yaml has a preset dependency") double-messages the operator:
// the rendered error becomes "preset is required: …: preset not
// found in Chart.yaml dependencies" — same fact twice. The fix is to
// return the inner error directly; its hint already covers both
// recovery paths (add a preset dependency OR pass --preset).
func TestInitUpdate_PresetNotFoundError_SingleWrapped(t *testing.T) {
	imageOrig := initCmdFlags.image
	presetOrig := initCmdFlags.preset
	rootOrig := Config.RootDir
	t.Cleanup(func() {
		initCmdFlags.image = imageOrig
		initCmdFlags.preset = presetOrig
		Config.RootDir = rootOrig
	})

	initCmdFlags.image = ""
	initCmdFlags.preset = ""

	dir := t.TempDir()
	Config.RootDir = dir

	// Chart.yaml carrying only the `talm` library dependency, no
	// preset chart. readChartYamlPreset will iterate the deps and
	// return its "preset not found" error.
	chartYaml := "apiVersion: v2\nname: test\nversion: 0.1.0\n" +
		"dependencies:\n" +
		"  - name: talm\n" +
		"    version: 0.1.0\n"
	if err := os.WriteFile(filepath.Join(dir, "Chart.yaml"), []byte(chartYaml), 0o644); err != nil {
		t.Fatalf("seed Chart.yaml: %v", err)
	}

	err := updateTalmLibraryChart()
	if err == nil {
		t.Fatal("expected --update without preset to error; got nil")
	}

	msg := err.Error()

	// Double-wrap signature: the outer wrap text leaks into the
	// rendered string. After the fix this substring is gone.
	if strings.Contains(msg, "preset is required:") {
		t.Errorf("error must be single-wrapped — found outer wrap text %q in:\n%s", "preset is required:", msg)
	}

	// The inner cause must survive untouched.
	if !strings.Contains(msg, "preset not found in Chart.yaml dependencies") {
		t.Errorf("error must surface the inner cause; got:\n%s", msg)
	}

	// The hint chain must still surface the recovery path.
	hints := errors.GetAllHints(err)
	if len(hints) == 0 {
		t.Fatalf("expected at least one hint guiding the operator on how to declare a preset; got bare error:\n%s", msg)
	}

	combined := strings.Join(hints, "\n")
	if !strings.Contains(combined, "add a preset chart") {
		t.Errorf("hint chain must mention adding a preset chart (or equivalent recovery); got:\n%s", combined)
	}
}

// TestValidateImageRefShape_RejectsMalformed pins the shape check
// that runs before any --image value reaches the preset rewrite.
// Catches operator typos (::malformed, no-slash:tag, trailing
// separator) at command-parse time, instead of leaving a corrupt
// values.yaml that fails deep inside configloader on the next
// `talm template` / `talm apply`.
func TestValidateImageRefShape_RejectsMalformed(t *testing.T) {
	tests := []struct {
		name string
		ref  string
		want bool
	}{
		{"empty colon-prefix", "::malformed", false},
		{"leading colon", ":foo/bar:tag", false},
		{"leading at", "@sha256:abc", false},
		{"leading slash", "/foo/bar:tag", false},
		{"trailing colon", "ghcr.io/foo/bar:", false},
		{"trailing slash", "ghcr.io/foo/bar/", false},
		{"trailing at", "ghcr.io/foo/bar@", false},
		{"no slash separator", "no-slash:tag", false},
		{"slash but no tag and no digest", "ghcr.io/foo/bar", false},
		{"slash with empty colon at end", "ghcr.io/foo/bar:", false},
		{"valid tagged ref", "ghcr.io/siderolabs/installer:v1.13.0", true},
		{"valid digest pin sha256", "ghcr.io/foo/bar@sha256:abc123def456", true},
		{"valid digest pin sha512", "ghcr.io/foo/bar@sha512:0123456789abcdef", true},
		{"valid factory ref", "factory.talos.dev/installer/abcd1234:v1.13.0", true},
		{"valid registry with port", "registry.local:5000/foo/installer:v1.12.6", true},
		{"valid cozystack ref", "ghcr.io/cozystack/cozystack/talos:v1.12.6", true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateImageRefShape(tc.ref)
			if tc.want && err != nil {
				t.Errorf("validateImageRefShape(%q) = %v, want nil", tc.ref, err)
			}

			if !tc.want && err == nil {
				t.Errorf("validateImageRefShape(%q) = nil, want error", tc.ref)
			}
		})
	}
}

// TestValidateImageOverride_RejectsMalformedAtPresetLevel pins the
// integration: malformed --image values are surfaced as a hint-
// bearing error from validateImageOverride too, BEFORE the preset
// existence check (so the user sees the malformed-ref signal even
// on a typo'd preset name).
func TestValidateImageOverride_RejectsMalformedAtPresetLevel(t *testing.T) {
	preset := map[string]string{
		"good/values.yaml": "image: \"original\"\n",
		"good/Chart.yaml":  "name: good\n",
	}

	err := validateImageOverride(preset, "good", "::malformed")
	if err == nil {
		t.Fatal("expected an error on malformed --image")
	}

	if !strings.Contains(err.Error(), "malformed") {
		t.Errorf("error should cite the offending value, got: %v", err)
	}
}

// TestValidateImageOverride pins the up-front mismatch detection that
// runs in initCmd.RunE before any file is written: --image must be
// rejected when the chosen preset has no top-level image: field, so
// the user does not end up with a half-written project that silently
// dropped the flag.
func TestValidateImageOverride(t *testing.T) {
	preset := map[string]string{
		"good/values.yaml":  "image: \"original\"\n",
		"good/Chart.yaml":   "name: good\n",
		"empty/values.yaml": "endpoint: \"https://example.invalid\"\n",
		"empty/Chart.yaml":  "name: empty\n",
	}

	t.Run("empty override skips validation", func(t *testing.T) {
		if err := validateImageOverride(preset, "good", ""); err != nil {
			t.Errorf("empty override must not error, got: %v", err)
		}
		if err := validateImageOverride(preset, "empty", ""); err != nil {
			t.Errorf("empty override must not error on a preset without image: either, got: %v", err)
		}
	})

	t.Run("override on preset with image: passes", func(t *testing.T) {
		if err := validateImageOverride(preset, "good", "factory.talos.dev/installer/abc:v1"); err != nil {
			t.Errorf("validation failed on a preset that does declare image:, got: %v", err)
		}
	})

	t.Run("override on preset without image: errors with preset name", func(t *testing.T) {
		err := validateImageOverride(preset, "empty", "factory.talos.dev/installer/abc:v1")
		if err == nil {
			t.Fatal("expected an error when --image is set but preset has no image: field")
		}
		if !strings.Contains(err.Error(), "empty") {
			t.Errorf("error should name the offending preset so the user can act on it, got: %v", err)
		}
	})
}

// TestWriteSecretsBundleToFile_StillRefusesOverwrite pins that
// writeSecretsBundleToFile still honors the --force gate after the
// redundant validateFileExists call was dropped — the gate now lives
// only inside writeSecureToDestination, and this test would fail if
// that inner check were ever removed too.

// TestResolveTalosconfigEndpoints_GlobalNonEmpty_UsesGlobal pins
// that when the operator passes --endpoints on talm init, the
// talosconfig generated for the new project carries those
// endpoints in its context, NOT a hardcoded loopback. Without
// this contract, init writes a talosconfig with
// `endpoints: - 127.0.0.1` and the operator's --endpoints flag
// is silently discarded.
func TestResolveTalosconfigEndpoints_GlobalNonEmpty_UsesGlobal(t *testing.T) {
	t.Parallel()

	got := resolveTalosconfigEndpoints([]string{"10.0.80.201"})

	if len(got) != 1 || got[0] != "10.0.80.201" {
		t.Errorf("non-empty --endpoints must propagate to talosconfig; got %v, want [10.0.80.201]", got)
	}
}

// TestResolveTalosconfigEndpoints_GlobalEmpty_UsesLoopbackFallback
// pins the fallback shape: when --endpoints is omitted the
// generated talosconfig still needs a non-empty endpoints list
// (yaml schema requirement), so we seed the loopback placeholder.
// The operator edits it to a real endpoint after init.
func TestResolveTalosconfigEndpoints_GlobalEmpty_UsesLoopbackFallback(t *testing.T) {
	t.Parallel()

	got := resolveTalosconfigEndpoints(nil)
	if len(got) != 1 || got[0] != defaultLocalEndpoint {
		t.Errorf("empty --endpoints must fall back to defaultLocalEndpoint; got %v, want [%s]", got, defaultLocalEndpoint)
	}
}

// TestResolveTalosconfigEndpoints_ReturnsCopy pins that the
// returned slice is not aliased with the caller's slice. Without
// this, downstream mutation of the talosconfig's endpoints would
// also mutate the operator-visible GlobalArgs.Endpoints between
// init runs in a single process (which the test harness exercises).
func TestResolveTalosconfigEndpoints_ReturnsCopy(t *testing.T) {
	t.Parallel()

	input := []string{"10.0.80.201"}

	got := resolveTalosconfigEndpoints(input)
	got[0] = "mutated"

	if input[0] == "mutated" {
		t.Errorf("returned slice must be a copy; mutating it also mutated the input (aliased)")
	}
}

// TestResolveTalosconfigEndpoints_MultipleEndpoints pins that all
// supplied endpoints propagate (StringSlice flag accepts repeated
// or comma-separated values). Operators with a HA cluster may
// pass multiple --endpoints on init.
func TestResolveTalosconfigEndpoints_MultipleEndpoints(t *testing.T) {
	t.Parallel()

	got := resolveTalosconfigEndpoints([]string{"10.0.80.201", "10.0.80.202", "10.0.80.203"})
	if len(got) != 3 {
		t.Fatalf("multi-endpoint input must all propagate; got %v", got)
	}

	want := []string{"10.0.80.201", "10.0.80.202", "10.0.80.203"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("endpoint[%d]: got %q, want %q", i, got[i], want[i])
		}
	}
}

func TestWriteSecretsBundleToFile_StillRefusesOverwrite(t *testing.T) {
	forceOrig := initCmdFlags.force
	rootOrig := Config.RootDir
	t.Cleanup(func() {
		initCmdFlags.force = forceOrig
		Config.RootDir = rootOrig
	})

	dir := t.TempDir()
	Config.RootDir = dir
	initCmdFlags.force = false

	// Seed a pre-existing secrets.yaml.
	existing := filepath.Join(dir, "secrets.yaml")
	if err := os.WriteFile(existing, []byte("preserve-me"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	err := writeSecretsBundleToFile(nil)
	if err == nil {
		t.Fatal("expected error refusing to overwrite existing secrets.yaml")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("error should mention existing-file gate, got: %v", err)
	}

	// Original content must be intact.
	got, readErr := os.ReadFile(existing)
	if readErr != nil {
		t.Fatalf("ReadFile: %v", readErr)
	}
	if string(got) != "preserve-me" {
		t.Errorf("original content changed: %q", got)
	}
}
