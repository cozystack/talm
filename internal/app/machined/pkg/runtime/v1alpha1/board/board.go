// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

// Package board provides the function to discover the current board.
package board

import (
	"fmt"

	"github.com/cozystack/talm/internal/app/machined/pkg/runtime"
	bananapim64 "github.com/cozystack/talm/internal/app/machined/pkg/runtime/v1alpha1/board/bananapi_m64"
	jetsonnano "github.com/cozystack/talm/internal/app/machined/pkg/runtime/v1alpha1/board/jetson_nano"
	libretechallh3cch5 "github.com/cozystack/talm/internal/app/machined/pkg/runtime/v1alpha1/board/libretech_all_h3_cc_h5"
	nanopir4s "github.com/cozystack/talm/internal/app/machined/pkg/runtime/v1alpha1/board/nanopi_r4s"
	"github.com/cozystack/talm/internal/app/machined/pkg/runtime/v1alpha1/board/pine64"
	"github.com/cozystack/talm/internal/app/machined/pkg/runtime/v1alpha1/board/rock64"
	rockpi4 "github.com/cozystack/talm/internal/app/machined/pkg/runtime/v1alpha1/board/rockpi4"
	rockpi4c "github.com/cozystack/talm/internal/app/machined/pkg/runtime/v1alpha1/board/rockpi4c"
	rpigeneric "github.com/cozystack/talm/internal/app/machined/pkg/runtime/v1alpha1/board/rpi_generic"
	"github.com/siderolabs/talos/pkg/machinery/constants"
)

// NewBoard initializes and returns a runtime.Board.
// Deprecated: Not supported anymore, use overlays instead.
func NewBoard(board string) (b runtime.Board, err error) {
	return newBoard(board)
}

//gocyclo:ignore
func newBoard(board string) (b runtime.Board, err error) {
	switch board {
	case constants.BoardLibretechAllH3CCH5:
		b = &libretechallh3cch5.LibretechAllH3CCH5{}
	case constants.BoardRPiGeneric:
		b = &rpigeneric.RPiGeneric{}
	case constants.BoardBananaPiM64:
		b = &bananapim64.BananaPiM64{}
	case constants.BoardPine64:
		b = &pine64.Pine64{}
	case constants.BoardRock64:
		b = &rock64.Rock64{}
	case constants.BoardRockpi4:
		b = &rockpi4.Rockpi4{}
	case constants.BoardRockpi4c:
		b = &rockpi4c.Rockpi4c{}
	case constants.BoardJetsonNano:
		b = &jetsonnano.JetsonNano{}
	case constants.BoardNanoPiR4S:
		b = &nanopir4s.NanoPiR4S{}
	default:
		return nil, fmt.Errorf("unsupported board: %q", board)
	}

	return b, nil
}
