# testfs static analyzer

`testfs/analyzer` is an optional `go/analysis` analyzer for potential runtime
filesystem dependencies in Go tests. Static findings are possibilities, not
observed I/O or reproduced cache misses. The primary `testfs` project interface
is runtime preserved-work inspection; see the [root README](../README.md) for
that workflow.

## Build and run

```sh
go build -o ./bin/testfs-analyzer ./analyzer/cmd/testfs-analyzer
./bin/testfs-analyzer ./...
go vet -vettool="$(pwd)/bin/testfs-analyzer" ./...
```

The command supports the standard singlechecker JSON output and Go vettool
handshake. Its diagnostic analyzer name remains `testfs` for compatibility.

```sh
./bin/testfs-analyzer -uncertain=false ./...
./bin/testfs-analyzer -json ./...
./bin/testfs-analyzer -report-dir ./testfs-reports ./...
```

`-report-dir` writes one versioned JSON report per test package, named by an
import-path hash. It uses the shared [`report.Finding`](../report/schema.go)
schema; use a fresh output directory because stale reports are not removed.

## Rules and limits

| Rule | Meaning |
| --- | --- |
| TFS001 | Potential disk-backed filesystem data or metadata access |
| TFS002 | Unresolved filesystem backing/path safety, or exhausted analysis budget |
| TFS003 | Write, creation, mutation, or write-mode open that may add a dependency |

The analyzer identifies test roots, examples, `TestMain`, test-file initializers,
and fuzz seed callbacks, then summarizes reachable type-resolved SSA helpers.
It models `os`, `io/fs`, `filepath`, templates, `embed.FS`, `fstest.MapFS`, and
real `testing` temporary-directory methods. Embedded content and proven
test-owned temporary paths are allowed; mixed or unresolved provenance remains
unsafe or uncertain. It does not report ordinary unreachable production code.

Analysis is bounded to 12 summary passes, 128 operations per function, and
provenance trees of depth 12 / 128 nodes. Reflection, unsafe, arbitrary heap
aliasing, unresolved dynamic dispatch, subprocess I/O, and arbitrary callbacks
are outside its coverage. Symlinks, unusual `TMPDIR` values, and unmodeled
mutations can invalidate temporary-path safety. See the detailed
[analyzer specification](go-test-filesystem-analyzer-spec.md) for its model and
non-goals.

## Embedding and metalinters

Import `testfs/analyzer` and use `analyzer.Analyzer`, or call `analyzer.New()`
for an independently configurable instance. A `multichecker` host can use it
directly. The host, rather than this analyzer, owns `//nolint:testfs` filtering.

See the [golangci-lint module adapter example](examples/golangci/README.md).
It keeps host-version dependencies outside this module.

## Performance and validation

```sh
go test -count=1 ./analyzer/...
go test -run '^$' -bench BenchmarkAnalyzer -benchmem ./analyzer
```

The benchmark and its baseline are documented in [PERFORMANCE.md](PERFORMANCE.md).
