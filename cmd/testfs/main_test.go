package main

import (
	"testing"
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
