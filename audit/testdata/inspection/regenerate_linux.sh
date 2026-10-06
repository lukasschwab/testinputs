#!/bin/sh
# Regenerate linux fixture deliberately; not used by normal tests.
set -eu
out=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
tmp=$(mktemp -d); trap 'rm -rf "$tmp"' EXIT
root="$tmp/checkout with spaces"; external="$tmp/external"; temporary="$tmp/temporary"
mkdir -p "$root/second" "$external" "$temporary" "$tmp/build"
cat >"$root/go.mod" <<'SRC'
module workfixture
go 1.27.0
SRC
cat >"$root/main_test.go" <<SRC
package workfixture_test
import("os";"testing")
func TestInputs(t *testing.T) { os.ReadFile("fixture with spaces.txt"); os.Stat("."); os.Open("missing file"); os.ReadFile("$external/outside.txt"); os.Open("$temporary/missing temporary file"); t.Chdir("$external"); os.ReadFile("outside.txt"); t.Chdir("$root") }
SRC
cat >"$root/second/second_test.go" <<'SRC'
package second
import "testing"
func TestNothing(*testing.T) {}
SRC
: >"$root/fixture with spaces.txt"; : >"$external/outside.txt"
# Review the emitted HASH[testInputs] lines before replacing expected-inputs.
GODEBUG=gocachehash=1 GOTMPDIR="$tmp/build" go test -work ./... 2>"$tmp/hash" >"$tmp/out" || true
work=$(sed -n 's/^WORK=//p' "$tmp/out")
go list -json ./... >"$out/packages.json"
# Copy only b*/_testmain.go and b*/testlog.txt from "$work", replace absolute
# paths with @ROOT@/@EXTERNAL@/@TEMP@, and update expected-inputs from $tmp/hash.
printf '%s\n' "Captured work is $work; inspect $tmp/hash, then curate $out/linux." >&2
