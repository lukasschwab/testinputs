// Package audit inspects package-level Go test cache inputs from preserved work directories.
package audit

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"testfs/report"
)

const ExitAuditFailure = 2

type Finding = report.Finding

type config struct {
	JSON, Work, PackagesJSON string
}

// Main reads only supplied artifacts. It never invokes Go, test binaries, or other processes.
func Main(args []string) int {
	var c config
	flags := flag.NewFlagSet("testfs", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	flags.Usage = func() {
		fmt.Fprintln(flags.Output(), "Usage: testfs -work DIR -packages-json packages.json [-json FILE|-]")
		fmt.Fprintln(flags.Output(), "Inspect a preserved go test -work directory without executing commands.")
		flags.PrintDefaults()
	}
	flags.StringVar(&c.JSON, "json", "", "write JSON report to FILE; - writes stdout")
	flags.StringVar(&c.Work, "work", "", "directory preserved by go test -work")
	flags.StringVar(&c.PackagesJSON, "packages-json", "", "metadata saved by go list -json")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return ExitAuditFailure
	}
	if len(flags.Args()) != 0 {
		fmt.Fprintln(os.Stderr, "testfs: package and execution arguments are unsupported; supply only artifacts")
		return ExitAuditFailure
	}
	if c.Work == "" || c.PackagesJSON == "" {
		fmt.Fprintln(os.Stderr, "testfs: both -work and -packages-json are required")
		return ExitAuditFailure
	}
	return inspectMain(c)
}

func writeReportJSON(c config, r InspectionReport) error {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	if c.JSON == "-" {
		_, err = os.Stdout.Write(b)
		return err
	}
	return os.WriteFile(c.JSON, b, 0600)
}
