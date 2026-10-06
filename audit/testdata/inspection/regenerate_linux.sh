#!/bin/sh
# Capture a Go 1.27 linux fixture for review. Normal tests never run this.
# Usage: regenerate_linux.sh OUTPUT_DIRECTORY
set -eu
[ $# -eq 1 ] || { echo "usage: $0 OUTPUT_DIRECTORY" >&2; exit 2; }
out=$1
mkdir -p "$out"
out=$(CDPATH= cd -- "$out" && pwd)
base=$(mktemp -d); root="$base/checkout with spaces"; external="$base/external"; temporary="$base/temporary"
mkdir -p "$root/second" "$external" "$temporary" "$base/build" "$base/cache"
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
# Hashing ignores files newer than the test start. Make inputs safely old.
touch -d '2 minutes ago' "$root/fixture with spaces.txt" "$external/outside.txt"
(
 cd "$root"
 GOFLAGS= GOWORK=off GOCACHE="$base/cache" GOTMPDIR="$base/build" GODEBUG=gocachehash=1 \
   go test -work ./... >"$out/go-test.stdout" 2>"$out/go-test.stderr"
 GOFLAGS= GOWORK=off go list -json ./... >"$out/packages.json.raw"
)
work=$(sed -n 's/^WORK=//p' "$out/go-test.stderr")
[ -n "$work" ] || { echo "no WORK directory in $out/go-test.stderr" >&2; exit 1; }
printf '%s\n' "$work" >"$out/WORK"
printf '%s\n' "Capture retained in $out; curate _testmain.go, testlog.txt, packages metadata, and HASH[testInputs] into the fixture. Source workspace is $base." >&2
