// Command fluxa-builder packages Fluxa projects for distribution.
package main

import (
	"os"

	"github.com/RodBarenco/fluxa-builder/internal/app"
)

func main() {
	if app.IsInstalledInvocation(os.Args[0]) {
		os.Exit(app.RunInstalled(os.Args[0], os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
	}
	os.Exit(app.Run(os.Args[1:], os.Stdout, os.Stderr))
}
