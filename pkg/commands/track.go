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
	"time"

	"github.com/spf13/cobra"
)

type trackableActionCmdFlags struct {
	wait    bool
	debug   bool
	timeout time.Duration
}

func (f *trackableActionCmdFlags) addTrackActionFlags(cmd *cobra.Command) {
	cmd.Flags().BoolVar(&f.wait, "wait", true, "wait for the operation to complete, tracking its progress. always set to true when --debug is set")
	cmd.Flags().BoolVar(&f.debug, "debug", false, "debug operation from kernel logs. --wait is set to true when this flag is set")
	cmd.Flags().DurationVar(&f.timeout, "timeout", 30*time.Minute, "time to wait for the operation is complete if --debug or --wait is set")
}
