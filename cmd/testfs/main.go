// Command testfs is a standard go/analysis singlechecker and go vet tool.
package main

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"

	"testfs"
	"testfs/audit"

	"golang.org/x/tools/go/analysis/singlechecker"
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "audit":
			os.Exit(audit.Main(os.Args[2:]))
		case "--testfs-launch":
			os.Exit(audit.Launch(os.Args[2:]))
		case "-V=full":
			// x/tools v0.50 prints the absolute executable path here. Go's
			// handshake parser cannot parse paths with spaces. Keep the
			// content-hash build ID and use a stable tool name instead.
			os.Exit(version())
		}
	}
	singlechecker.Main(testfs.Analyzer)
}

func version() int {
	filename, err := os.Executable()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	f, err := os.Open(filename)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer f.Close()
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Printf("testfs version devel buildID=%x\n", h.Sum(nil))
	return 0
}
