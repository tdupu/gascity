package main

import (
	"fmt"
	"io"
	"strings"
	"unicode"

	"github.com/gastownhall/gascity/internal/beadmeta"
	"github.com/spf13/cobra"
)

// originUserValue is the value stamped by `gc beads promote` to mark a bead as
// user-origin. The key is declared in internal/beadmeta; the value is the
// open-world string agreed by the B.4 pack formula and C.5 binary subcommand.
const originUserValue = "user"

func newBeadsPromoteCmd(stdout, stderr io.Writer) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "promote <bead-id>",
		Short: "Promote a bead to user-origin status",
		Long: `Promote a bead to user-origin status by stamping gc.origin=user metadata.

Delegates to bd update <bead-id> --set-metadata gc.origin=user, routing to the
correct rig store automatically (same scope resolution as gc bd).

gc.origin is an open KV metadata field. The "user" value marks the bead as
having been explicitly claimed by a human, distinguishing it from engine-created
infrastructure beads. This mirrors the gc.routed_to / gc.halt_chain keys already
written by gascity-packs.`,
		Example: `  gc beads promote ga-abc
  gc beads promote my-project-xyz`,
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if doBeadsPromote(args[0], stdout, stderr) != 0 {
				return errExit
			}
			return nil
		},
	}
	return cmd
}

// doBeadsPromote stamps gc.origin=user on the given bead ID by delegating to
// doBd with the equivalent `bd update <id> --set-metadata gc.origin=user` args.
// All rig routing, silent-fallback detection, and exact-ID guards from doBd
// apply automatically — promote is a thin naming layer over the update path.
func doBeadsPromote(beadID string, stdout, stderr io.Writer) int {
	const cmdName = "gc beads promote"

	beadID = strings.TrimSpace(beadID)
	if beadID == "" {
		fmt.Fprintln(stderr, cmdName+": bead-id must not be empty") //nolint:errcheck // best-effort stderr
		return 1
	}
	// Reject whitespace-containing or flag-shaped ids the same way doBd's
	// heartbeat rewriter does, to prevent accidental injection into bd args.
	if strings.IndexFunc(beadID, unicode.IsSpace) >= 0 || strings.HasPrefix(beadID, "-") {
		fmt.Fprintf(stderr, "%s: invalid bead-id %q\n", cmdName, beadID) //nolint:errcheck // best-effort stderr
		return 1
	}

	metadataArg := beadmeta.OriginMetadataKey + "=" + originUserValue
	bdArgs := []string{"update", beadID, "--set-metadata", metadataArg}

	return doBd(bdArgs, stdout, stderr)
}
