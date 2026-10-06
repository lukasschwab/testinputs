# testfs

`testfs` analyzes preserved Go test work directories to identify runtime filesystem
inputs that can make test-result caching sensitive to a checkout. Its primary mode
is inspection: it reads Go's saved test logs and **does not execute tests**.

Requires Go 1.27. Runtime inspection supports released Go 1.27.x toolchains on
Linux, macOS, and Windows. It reports observed cache inputs, not cache misses or
proof that a test suite is hermetic.

## Inspect preserved work (primary workflow)

Build the runtime tool, preserve a work directory while running the tests, then
inspect that directory. Do **not** add `-count=1`: doing so disables Go's normal
cache-input logging.

```sh
go build -o ./bin/testfs ./cmd/testfs
go test -work ./...
# Go prints WORK=/path/to/work
./bin/testfs -work /path/to/work -project "$PWD"
# Stream exactly one indented inspection report to stdout.
./bin/testfs -work /path/to/work -project "$PWD" -json -
```

`-work` never runs tests. It reads identities from generated `_testmain.go` files
and package roots from `go list`. Save package metadata when inspection must work
offline or later:

```sh
go list -json ./... > packages.json
./bin/testfs -work /path/to/work -packages-json packages.json -json inspection.json
```

Pass original package-selection build settings via repeatable
`-build-flag`, for example `-build-flag=-tags=integration`. Metadata, preserved
logs, checkout paths, and symlink layout must retain the spelling and topology
used by the test. Status 0 is clean, 1 means a cache-relevant input was observed,
and 2 means inspection was incomplete. `-json -` writes exactly one indented
inspection JSON document to stdout; report coverage errors remain in that document
while operational errors are written to stderr.

Absent actions (for example cache hits, skipped packages, or disabled logging) do
not show that a test is cache-independent. The inspector filters Go 1.27 logged
opens/stats and `chdir` operations according to Go's lexical-then-symlink
behavior. It does not calculate hashes, judge cache eligibility, or attribute an
operation to a test. Initialization and pre-`m.Run` setup, subprocess/direct
syscall I/O, outcomes, and read/write mode are not present in these logs;
environment records are intentionally ignored.

Inspection fixtures and their regeneration procedure live in
[`audit/testdata/inspection`](audit/testdata/inspection/README.md).

## Collect a fresh runtime audit

A collection run is available when no preserved work directory exists. It runs
selected tests with `-count=1`, so it measures observed access rather than cache
hits. The historic `testfs audit` spelling remains an alias. During fresh collection,
stdout remains reserved for child test output, so `-json -` is intentionally
available only with `-work`.

```sh
./bin/testfs ./...
./bin/testfs -json audit.json -- -race -run TestAttachment ./...
./bin/testfs -temp-base /an/existing/external/directory -- ./...
./bin/testfs audit -fail-on-checkout -keep-logs -- ./...
```

Options precede `--`; arguments after it go to `go test`. Collection preserves
test output and its nonzero status, while collector failures return 2 and
`-fail-on-checkout` returns 3 after otherwise successful tests. `-temp-base`
creates a test-owned directory outside selected roots and sets `TMPDIR`, `TMP`,
and `TEMP`; it is removed after collection. `-keep-logs` retains raw logs and
invocation metadata.

The internal Go log records `open`, `stat`, and `chdir`. An open is an attempt,
including failed and write-only opens, so results never claim a confirmed read.
Missing, malformed, truncated, or interrupted collection is not clean. Active
fuzzing, benchmarks, compile-only/list-only invocations, and overrides of
`-exec`, `-test.testlogfile`, or `-count` are rejected.

## Optional static analyzer

The source analyzer is intentionally contained in [`analyzer`](analyzer/). It is
not part of the default `testfs` command or runtime inspection dependency graph.
See its [README](analyzer/README.md) to build the optional vettool, embed
`testfs/analyzer`, or use the golangci-lint adapter example.

## Validation

```sh
go test -count=1 ./...
go test -race -count=1 ./...
go vet ./...
```

The shared versioned finding schema is in [`report`](report/schema.go). Runtime
implementation references include [Go test logging](https://go.dev/src/testing/internal/testdeps/deps.go)
and [Go test cache inputs](https://go.dev/src/cmd/go/internal/test/test.go).
