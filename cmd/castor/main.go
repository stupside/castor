// Command castor casts over the public API, composing missing services in its own process.
package main

import (
	"slices"

	"github.com/urfave/cli/v3"

	"github.com/stupside/castor/cmd/internal/process"
	"github.com/stupside/castor/services/apiserver"
	"github.com/stupside/castor/services/mediaserver"
	"github.com/stupside/castor/services/scrapingserver"
)

func main() {
	local := Local(apiserver.Embedded(mediaserver.Embedded, scrapingserver.Embedded))
	process.Run(&cli.Command{
		Name:     "castor",
		Usage:    "Cast video streams to networked devices",
		Commands: slices.Concat(Commands(local), []*cli.Command{apiserver.Command(), mediaserver.Command(), scrapingserver.Command()}),
	})
}
