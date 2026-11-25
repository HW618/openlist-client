// Command openlist-client is a lightweight cross-platform client for
// OpenList (https://github.com/OpenListTeam/OpenList), written with NSGo.
package main

import (
	"github.com/nsgo-dev/nsgo"
	"github.com/nsgo-dev/nsgo/core"

	"github.com/HW618/openlist-client/src/app"
)

func main() {
	nsgo.Run(func(cx *core.Scope) { app.Run(cx, nsgo.SetMainView) }, nsgo.WindowSize(960, 720))
}
