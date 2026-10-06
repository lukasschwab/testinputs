# Preserved-work golden provenance

`linux` was captured with Go 1.27.0 on linux/amd64.  It contains only generated
`_testmain.go`, test logs, saved package metadata, and the independently captured
cache-input operation/path records; it intentionally contains no build artifacts.
`@ROOT@`, `@EXTERNAL@`, and `@TEMP@` are rebound by the test, preserving path
spelling (including spaces).

To refresh deliberately after a supported-toolchain update, create the documented
fixture scenario from `scripts/check-test-workdir_test.go`, run `GODEBUG=gocachehash=1
go test -work`, copy only those artifacts, and derive `expected-inputs.txt` from
`HASH[testInputs]` records (strip each trailing hash). Then run:

```sh
nice -n 10 go test -short -count=1 ./scripts
```

This is an explicit regeneration/verification procedure, not part of normal tests.
