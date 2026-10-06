// Command testinputs-analyzer runs the optional testinputs static analyzer.
package main

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"

	"github.com/lukasschwab/testinputs/analyzer"

	"golang.org/x/tools/go/analysis/singlechecker"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "-V=full" {
		os.Exit(version())
	}
	singlechecker.Main(analyzer.Analyzer)
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
	if _, err := io.Copy(h, f); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	// x/tools' handshake parser needs a stable name rather than an absolute path.
	fmt.Printf("testinputs version devel buildID=%x\n", h.Sum(nil))
	return 0
}
