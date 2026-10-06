# testfs

`testfs` analyzes preserved Go test work directories to identify filesystem and
environment inputs that can affect test-result caching. It reads supplied
artifacts and **does not execute commands**.

Requires Go 1.27 test-work artifacts. The inspector understands the Go 1.27
test-log format on Linux, macOS, and Windows. It reports observed cache inputs,
not cache misses or proof that a test suite is hermetic.

## Inspect preserved work

`testfs` is artifact-only: it never invokes `go`, test binaries, or any other
process. Supply both artifacts from the same checkout:

```sh
go build -o ./bin/testfs ./cmd/testfs
go list -json ./... > packages.json
go test -work ./...
# Go prints WORK=/path/to/work
./bin/testfs -work /path/to/work -packages-json packages.json -json -
```

Collect logs without `-count=1`, which disables Go's automatic cache-input logging.
Use the same package selection and build settings for metadata collection and testing.

Both `-work` and `-packages-json` are required. Metadata must describe the exact
checkout paths used by the test run, including symlink spelling. `-json -` writes
one report to stdout; omitting `-json` prints a readable report there. The command
does not accept package selectors, build flags, or test arguments, and it never
falls back to `go list`.

The inspector reads package identities from generated `_testmain.go` files and
reports Go 1.27 test-log cache inputs: filesystem `open`, `stat`, and `chdir`
events, environment names (never values), and the implicit `GODEBUG` input.
Findings are package-level observations, not cache misses or per-test attribution.
It does not calculate hashes or judge cache eligibility.

Exit 1 means a cache-relevant observation was found. Every complete compatible
action includes the implicit `GODEBUG` input, even if no environment read was
logged, so a complete inspection normally exits 1. Exit 0 means no findings. Missing actions/logs,
malformed logs, invalid identities, and invalid metadata are coverage errors and
exit 2, taking precedence over findings. Cache hits and skipped packages can leave
no artifacts and cannot prove cache independence. Initialization before `m.Run`,
child processes, direct syscalls, operation outcomes, and read/write mode are not
represented by the logs.

Inspection fixtures and their external regeneration procedure live in
[`audit/testdata/inspection`](audit/testdata/inspection/README.md).

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

The shared versioned finding schema is in [`report`](report/schema.go) (schema
version 2 adds the distinct `environment` finding field; filesystem findings
continue to use `path` and `class`). Runtime
implementation references include [Go test logging](https://go.dev/src/testing/internal/testdeps/deps.go)
and [Go test cache inputs](https://go.dev/src/cmd/go/internal/test/test.go).
