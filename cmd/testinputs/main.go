// Command testinputs inspects supplied Go test work artifacts.
package main

import (
	"github.com/lukasschwab/testinputs/audit"
	"os"
)

func main() { os.Exit(run(os.Args[1:])) }

// run retains "audit" as a harmless spelling for the artifact inspector.
func run(args []string) int {
	if len(args) == 0 {
		return audit.Main([]string{"-h"})
	}
	if args[0] == "audit" {
		return audit.Main(args[1:])
	}
	return audit.Main(args)
}
