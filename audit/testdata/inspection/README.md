# Inspection fixture provenance

`linux` is a minimal captured Go 1.27.0 linux/amd64 `go test -work` directory.
`@ROOT@`, `@EXTERNAL@`, and `@TEMP@` are rebound consistently by
`inspect_test.go`. `expected-inputs.txt` and `expected-environments.txt` are independently
selected from the `HASH[testInputs]` diagnostic emitted by
`GODEBUG=gocachehash=1`, not derived from this inspector. The environment file
is cross-checked against captured `getenv` records and keeps those names separate
from cmd/go's implicit `GODEBUG` input. Neither fixture contains an environment
value or hash. No binaries are kept.

To deliberately update Go support, run `regenerate_linux.sh` to recreate its
disposable scenario, including the fixture's explicit environment reads. It runs
`go test -work` with `GODEBUG=gocachehash=1`; copy only generated
`_testmain.go`, `testlog.txt`, and saved `go list -json` metadata. Select
filesystem and environment **names** from `HASH[testInputs]`, cross-check logged
environment names against `testlog.txt`, and never copy values or hashes. This is
a developer regeneration procedure; normal tests invoke neither `go test` nor
`go list`.
