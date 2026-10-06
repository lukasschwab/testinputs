# Inspection fixture provenance

`linux` is a minimal captured Go 1.27.0 linux/amd64 `go test -work` directory.
`@ROOT@`, `@EXTERNAL@`, and `@TEMP@` are rebound consistently by
`inspect_test.go`. `expected-inputs.txt` and `expected-environments.txt` are independent captures
from `GODEBUG=gocachehash=1`, not derived from this inspector. The latter keeps
logged environment names separate from cmd/go's implicit `GODEBUG` input; neither
fixture contains an environment value or hash. No binaries are kept.

To deliberately update Go support, recreate the disposable scenario described
in the expected-input comments with `go test -work` and `GODEBUG=gocachehash=1`,
then copy only generated `_testmain.go`, `testlog.txt`, and saved `go list -json`
metadata. Compare selected hash inputs before updating this fixture. This is a
developer regeneration procedure; normal tests invoke neither `go test` nor
`go list`.
