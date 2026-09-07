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

package engine_test

import (
	"testing"

	"helm.sh/helm/v4/pkg/strvals"
)

// TestSetValueDotsNestOnlyInTheKey pins what --set actually does with a
// dotted value, because talm used to warn that it did something else:
// that `--set endpoint=10.0.0.1` rendered as {endpoint: {10: {0: {0: 1}}}}
// and that operators had to reach for --set-string to avoid it.
//
// It never did. strvals' key scanner stops at '=', so a dot only starts a
// new nesting level on the left of it; the value scanner stops at ',' and
// hands whatever it read to typedVal, which falls back to the raw string
// once ParseInt fails. Verified identical on helm v3.15.2 and v4, so this
// is not something the v3-to-v4 migration changed.
//
// What --set does convert is integers and booleans. That is the real
// reason to prefer --set-string, and the only one worth documenting.
func TestSetValueDotsNestOnlyInTheKey(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		set  string
		key  string
		want any
	}{
		{"ipv4 literal stays a string", "endpoint=10.0.0.1", "endpoint", "10.0.0.1"},
		{"cidr stays a string", "cidr=10.0.0.0/24", "cidr", "10.0.0.0/24"},
		{"version stays a string", "talosVersion=v1.13.0", "talosVersion", "v1.13.0"},
		{"bare version stays a string", "talosVersion=1.13", "talosVersion", "1.13"},
		{"integers are converted", "port=8080", "port", int64(8080)},
		{"booleans are converted", "enabled=true", "enabled", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := map[string]any{}
			if err := strvals.ParseInto(tc.set, got); err != nil {
				t.Fatalf("ParseInto(%q): %v", tc.set, err)
			}

			if got[tc.key] != tc.want {
				t.Errorf("--set %s produced %s = %#v (%T), want %#v (%T)",
					tc.set, tc.key, got[tc.key], got[tc.key], tc.want, tc.want)
			}
		})
	}

	// A dot on the left of '=' does nest, which is the behaviour the
	// warning mistook for value handling.
	nested := map[string]any{}
	if err := strvals.ParseInto("a.b=10.0.0.1", nested); err != nil {
		t.Fatalf("ParseInto nested key: %v", err)
	}

	inner, ok := nested["a"].(map[string]any)
	if !ok {
		t.Fatalf("--set a.b=... produced %#v, want a nested map under \"a\"", nested["a"])
	}

	if inner["b"] != "10.0.0.1" {
		t.Errorf("--set a.b=10.0.0.1 produced a.b = %#v, want \"10.0.0.1\"", inner["b"])
	}
}
