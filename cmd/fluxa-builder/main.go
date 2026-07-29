// Command fluxa-builder packages Fluxa projects for distribution.
package main

import (
	"os"

	"github.com/RodBarenco/fluxa-builder/internal/app"
)

func main() {
	os.Exit(app.Run(os.Args[1:], os.Stdout, os.Stderr))
}
