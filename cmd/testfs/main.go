// Command testfs inspects preserved Go test work directories and can collect a fresh runtime audit.
package main

import (
	"os"

	"testfs/audit"
)

func main() { os.Exit(run(os.Args[1:])) }

func run(args []string) int {
	if len(args) == 0 {
		return audit.Main([]string{"-h"})
	}
	if len(args) > 0 {
		switch args[0] {
		case "audit": // Kept as an alias for existing runtime-audit users.
			return audit.Main(args[1:])
		case "--testfs-launch":
			return audit.Launch(args[1:])
		}
	}
	return audit.Main(args)
}
