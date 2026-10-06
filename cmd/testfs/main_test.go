package main

import (
	"testing"

	"testfs/audit"
)

func TestNoArgumentsShowsHelp(t *testing.T) {
	if code := run(nil); code != 0 {
		t.Fatalf("no-argument usage exited %d", code)
	}
}

func TestAuditAliasHelp(t *testing.T) {
	if code := run([]string{"audit", "-h"}); code != 0 {
		t.Fatalf("audit alias help exited %d", code)
	}
}

func TestArtifactInputsAreRequired(t *testing.T) {
	for _, args := range [][]string{{"-work", "/tmp/work"}, {"-packages-json", "packages.json"}, {"./..."}} {
		if code := run(args); code != audit.ExitAuditFailure {
			t.Fatalf("run(%q) = %d, want input failure", args, code)
		}
	}
}
