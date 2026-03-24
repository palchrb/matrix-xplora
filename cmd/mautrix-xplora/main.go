package main

import (
	"github.com/palchrb/matrix-xplora/pkg/connector"
	"maunium.net/go/mautrix/bridgev2/matrix/mxmain"
)

// Version variables are set at build time by the linker flags in build.sh.
var (
	Tag       = "unknown"
	Commit    = "unknown"
	BuildTime = "unknown"
)

func main() {
	m := mxmain.BridgeMain{
		Name:        "mautrix-xplora",
		Description: "A Matrix bridge for Xplora children's smartwatches",
		URL:         "https://github.com/palchrb/matrix-xplora",
		Version:     "0.1.0",
		Connector:   &connector.XploraConnector{},
	}
	m.InitVersion(Tag, Commit, BuildTime)
	m.PostInit = func() {
		connector.RegisterCommands(m.Bridge)
	}
	m.Run()
}
