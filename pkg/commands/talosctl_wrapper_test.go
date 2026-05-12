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
	"strings"
	"testing"

	"github.com/cockroachdb/errors"
	"github.com/spf13/cobra"
)

func TestContract_WrapDmesgCommand_TailCountGetsActionableHint(t *testing.T) {
	var tail bool

	sourceCmd := &cobra.Command{
		Use: "dmesg",
		Run: func(_ *cobra.Command, _ []string) {
			t.Fatal("dmesg command should not run when --tail has a non-bool value")
		},
	}
	sourceCmd.Flags().BoolVar(&tail, "tail", false, "specify if only new messages should be sent")

	wrappedCmd := wrapTalosCommand(sourceCmd, "dmesg")
	wrappedCmd.SetArgs([]string{"--tail=3"})

	err := wrappedCmd.Execute()
	if err == nil {
		t.Fatal("expected --tail=3 to fail")
	}
	if got := err.Error(); !strings.Contains(got, "--tail is a boolean") || strings.Contains(got, "strconv.ParseBool") {
		t.Fatalf("expected actionable --tail error without pflag internals, got: %v", err)
	}

	hints := strings.Join(errors.GetAllHints(err), "\n")
	for _, want := range []string{
		"talm dmesg --nodes <node> | tail -n 3",
		"talm dmesg --follow --tail",
	} {
		if !strings.Contains(hints, want) {
			t.Errorf("expected hint %q in:\n%s", want, hints)
		}
	}
}

func TestContract_WrapDmesgCommand_OtherFlagErrorsStayUnchanged(t *testing.T) {
	sourceCmd := &cobra.Command{
		Use: "dmesg",
		Run: func(_ *cobra.Command, _ []string) {
			t.Fatal("dmesg command should not run when an unknown flag is present")
		},
	}

	wrappedCmd := wrapTalosCommand(sourceCmd, "dmesg")
	wrappedCmd.SetArgs([]string{"--unknown"})

	err := wrappedCmd.Execute()
	if err == nil {
		t.Fatal("expected unknown flag to fail")
	}
	if got := err.Error(); !strings.Contains(got, "unknown flag") || strings.Contains(got, "--tail is a boolean") {
		t.Fatalf("expected ordinary cobra flag error, got: %v", err)
	}
}

func TestContract_DmesgTailLineCountFromError(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "numeric",
			in:   `invalid argument "12" for "--tail" flag: strconv.ParseBool: parsing "12": invalid syntax`,
			want: "12",
		},
		{
			name: "non_numeric",
			in:   `invalid argument "recent" for "--tail" flag: strconv.ParseBool: parsing "recent": invalid syntax`,
			want: "N",
		},
		{
			name: "unrelated",
			in:   `unknown flag: --tail-lines`,
			want: "N",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := dmesgTailLineCountFromError(tc.in); got != tc.want {
				t.Errorf("dmesgTailLineCountFromError() = %q, want %q", got, tc.want)
			}
		})
	}
}
