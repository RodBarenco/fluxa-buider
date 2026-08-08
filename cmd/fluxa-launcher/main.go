// Command fluxa-launcher is the executable an end user double-clicks in a
// portable application Fluxa Builder assembled. It is deliberately its own
// tiny program rather than the Fluxa Builder binary renamed: an
// application built for one platform on another needs a launcher compiled
// for the *target*, which the running Builder executable can never be —
// see docs/adr/0029-cross-target-application-launcher.md.
//
// It takes no arguments of its own; everything it needs it finds beside
// itself. internal/launcher holds all the logic, unchanged from when it
// lived inside internal/app.
package main

import (
	"os"

	"github.com/RodBarenco/fluxa-builder/internal/launcher"
)

func main() {
	os.Exit(launcher.Run(os.Args[0], os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
