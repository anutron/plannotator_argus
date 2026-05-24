// Command plannotator-argus is an argus plugin daemon that wraps the
// Plannotator CLI so sandboxed argus tasks can drive annotation/review
// flows.
//
// See openspec/changes/plannotator-argus-plugin/design.md for the design
// doc and openspec/changes/plannotator-argus-plugin/specs/.../spec.md for
// the behavioral requirements.
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// Version is the binary version. Set via -ldflags at build time:
//
//	go build -ldflags "-X main.Version=v0.1.0"
var Version = "dev"

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "plannotator-argus",
		Short:         "Argus plugin daemon that wraps the Plannotator CLI",
		Long:          "plannotator-argus is an argus plugin daemon. It registers MCP tools with argus and a POST /hook HTTP endpoint, then shells out to the user's existing `plannotator` binary on every call. The daemon process runs outside any argus task sandbox so Plannotator's local-browser handshake and session-file writes succeed normally.",
		Version:       Version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	root.AddCommand(newStartCmd())
	root.AddCommand(newStopCmd())
	root.AddCommand(newStatusCmd())

	return root
}

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "plannotator-argus: %v\n", err)
		os.Exit(1)
	}
}
