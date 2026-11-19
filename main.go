// Command openlist-client is a lightweight cross-platform client for
// OpenList (https://github.com/OpenListTeam/OpenList), written with NSGo.
package main

import (
	"github.com/nsgo-dev/nsgo"

	"github.com/HW618/openlist-client/src/app"
)

func main() { nsgo.Run(app.Root, nsgo.WindowSize(960, 720)) }
