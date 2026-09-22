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

	"github.com/spf13/cobra"
)

// The --image flag comes from upstream, whose help names a factory image as
// the default. talm resolves the target from values.yaml whenever -f is given
// without an explicit --image, so the inherited text describes a default that
// the documented flow never reaches.
func TestContract_UpgradeImageFlagHelpNamesTheResolution(t *testing.T) {
	cmd := &cobra.Command{Use: "upgrade"}
	cmd.Flags().StringP("image", "i", "factory.talos.dev/metal-installer/abc:v1.14.0",
		"the container image to use for performing the install")

	wrapUpgradeCommand(cmd, nil)

	usage := cmd.Flags().Lookup("image").Usage
	if !strings.Contains(usage, "values.yaml") {
		t.Errorf("--image help does not mention where the target actually comes from:\n%s", usage)
	}
}
