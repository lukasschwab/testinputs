# Inspection fixture provenance

The files here are minimal captured Go 1.27 `go test -work` artifacts. `@ROOT@`
is rebound by `inspect_test.go`; `/OUTSIDE` is rebound to a sibling outside that
root. Expected records in the test are an independent oracle captured from
`GODEBUG=gocachehash=1`, not derived from the inspector.

To regenerate after deliberately changing the supported Go implementation,
create the equivalent disposable module, run Go 1.27 with `go test -work` and
`GODEBUG=gocachehash=1`, copy only `_testmain.go`, `testlog.txt`, and `go list
-json` metadata (never binaries), then compare selected `HASH[testInputs]`
records before updating `inspect_test.go`. This is a developer procedure, not a
routine test dependency.
