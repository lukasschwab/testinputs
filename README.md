# testfs

`testfs` finds potential runtime filesystem dependencies in Go tests. It includes
a standard `go/analysis` analyzer, a `singlechecker` executable usable as a vettool,
and an independent runtime audit exposed as `testfs audit`.

Requires Go 1.27. The runtime adapter accepts released Go 1.27.x toolchains on
Linux, macOS, and Windows; other versions fail explicitly. Local validation was
performed on macOS/arm64 with Go 1.27.0. The CI matrix exercises all three operating
systems; cross-compilation alone does not establish runtime compatibility.

## Build and run

```sh
go build -o ./bin/testfs ./cmd/testfs
./bin/testfs ./...
go vet -vettool="$(pwd)/bin/testfs" ./...
```

Run either static command from the repository being analyzed, using the absolute
path to the built executable when it lives elsewhere. Test variants, including
external test packages, are handled by the standard Go analysis driver. Findings
make the static command exit unsuccessfully. No source files are changed.

```sh
# Omit uncertain TFS002 diagnostics (writes still have their own rule).
./bin/testfs -uncertain=false ./...

# Standard go/analysis JSON diagnostic envelope.
./bin/testfs -json ./...

# Versioned testfs evidence, one JSON file per test package.
./bin/testfs -report-dir ./testfs-reports ./...
```

The report filenames hash the package import path. Each report includes the
package name, schema version, findings, unresolved-call count, and limitations.
Use a fresh output directory per run: old package reports are not deleted.
`-json` remains the standard driver format; `-report-dir` uses the shared
[`report.Finding`](report/schema.go) schema also used by the audit.

## Static rules and analysis

| Rule | Meaning |
| --- | --- |
| TFS001 | Potential access to disk-backed filesystem data or metadata |
| TFS002 | Filesystem backing store/path safety is unresolved, or analysis exceeded its budget |
| TFS003 | Write, creation, mutation, or write-mode open that may add a dependency |

Diagnostics include operation and confidence. They identify potential
dependencies, not actual I/O or a demonstrated cache miss. A write is never
described as a read. Unknown `OpenFile` flags are conservatively treated as a
potential write. No automatic fixes or name-based suppressions are provided.

Roots are ordinary tests, examples, `TestMain`, test-file initializers, and fuzz
seed callbacks. Benchmarks are outside this initial scope. Reachable helpers are
summarized using type-resolved SSA operations. Production functions that are not
reachable from test roots do not produce test diagnostics.

The analyzer tracks provenance sets through interfaces, tuples, local calls,
closures, fields, and control-flow merges. A mixed disk/temporary origin stays
unsafe. Parameterized summaries and versioned `analysis.Fact` values preserve
argument-to-operation and argument-to-return relationships across package
boundaries. Findings are placed at the test call site, with a related helper
location when that source is part of the same analysis pass.

Model version 1 covers:

- `os` reads, opens, metadata, directory access, writes, temporary constructors,
  `DirFS`, and the modeled `os.Root` operations in [models.go](models.go).
- `io/fs` reads, directory operations, `Open`, and `Sub`.
- `filepath` glob/traversal and path joins; template `ParseFiles`, `ParseGlob`,
  and `ParseFS` functions and methods.
- `embed.FS`, `fstest.MapFS`, actual `testing` temporary-directory methods,
  `testing` callbacks, and filesystem wrappers with available `Open` summaries.

An unused `os.DirFS` constructor is not a finding. Reading embedded content and
writing it beneath a test-owned temporary root is allowed. Copying from a disk
fixture still reports the original read. `os.TempDir()` by itself does not prove
ownership of an existing file. Temporary joins must have constant, contained
components; escapes and unproven dynamic components lose safety. Known symlink
and rename/link mutations conservatively invalidate temporary safety in the
containing test summary.

Analysis is bounded to 12 summary passes, 128 operations per function, and
provenance trees of depth 12 / 128 nodes. Exceeding these bounds retains uncertainty
and reports incomplete coverage. This is not whole-program pointer analysis:
reflection, unsafe, arbitrary heap aliases, function-valued parameters, unresolved
interface dispatch, and arbitrary callbacks passed into libraries can be missed.
Address-taken local stores are merged conservatively, so a later mutation can
also qualify an earlier captured use. Unknown library calls are counted as
coverage limitations, not invented filesystem sinks. The unresolved-call count
counts direct sites and helper-summary boundaries, not unique whole-program calls.

Temporary paths are an intended testing pattern, not a security boundary or an
unconditional caching guarantee. An unusual `TMPDIR` inside the module, symlinks,
and unmodeled mutations can still affect caching. The analyzer does not alter
build inputs or Go's behavior when fixture contents change.

## Runtime audit

To inspect an **existing `go test -work` directory**, use the standalone script
[scripts/check-test-workdir.go](scripts/check-test-workdir.go). It uses only the
Go standard library and can be copied to another repository:

```sh
# Run in the repository being checked; note the printed WORK=... directory.
go test -work ./...

go run /path/to/testfs/scripts/check-test-workdir.go \
  -work /path/printed/as/WORK -project "$PWD"
```

This flags **package-level CI cache risks**: fixture/source opens, metadata and
directory observations beneath Go's package root, and every logged `chdir`.
A fresh checkout may change their metadata even if contents are identical.
The report suggests embedding fixtures, writing embedded data to `t.TempDir()`
when disk input is necessary, and avoiding unnecessary working-directory changes.
Intentional source-contract tests may reasonably retain the reported dependency.

The script reads package identities from generated `_testmain.go` files and gets
`Dir` and `Root` from `go list`. It follows Go 1.27's exact filtering order:
lexical containment first, then symlink fallbacks. External temporary-file opens
and stats are excluded; temporary files inside the package root are included.
`chdir` is included even outside that root. The script does not execute tests or
modify the work directory, and does not infer individual test names from the log.

Pass `-json` for machine-readable output. Use the original project/build settings;
for example, add `-build-flag=-tags=integration`. For reproducible offline metadata
resolution, save the original package metadata and provide it explicitly:

```sh
go list -json ./... > packages.json
go run /path/to/testfs/scripts/check-test-workdir.go \
  -work /path/printed/as/WORK -packages-json packages.json -json
```

Metadata must describe the same checkout paths as the test execution, including
any symlink spelling. Missing or malformed logs/identities/metadata are coverage
errors. Cache-hit packages may leave no actions at all, so the report makes claims
only about the preserved logs. **Do not use `-count=1` to collect these logs**:
it disables Go's automatic test-cache logging. `testfs audit` below supplies its
own logger flag when forcing execution.

For CI, build the script to preserve its exact exit codes (`go run` itself maps
nonzero program exits to status 1):

```sh
go build -o ./bin/check-test-workdir ./scripts/check-test-workdir.go
./bin/check-test-workdir -work /path/printed/as/WORK -project /path/to/repo
```

Exit 0 means no relevant filesystem operations in the inspected logs; 1 means
potential cache risks; 2 means incomplete/invalid inspection, taking precedence
over findings. This checks dependency selection rather than reproducing a cache
miss or calculating a cache key. It ignores environment records. Tests compare
its selected paths with Go's actual cache-input hashing output.

### Collect a fresh audit

```sh
./bin/testfs audit ./...
./bin/testfs audit -json audit.json -- -race -run TestAttachment ./...
./bin/testfs audit -temp-base /an/existing/external/directory -- ./...
./bin/testfs audit -fail-on-checkout -keep-logs -- ./...
```

Audit options precede `--`; arguments after it are forwarded to `go test`.
`-go /path/to/go` selects the Go command. Toolchain selection still follows Go's
normal configuration. Package metadata comes from `go list` with the same build
settings. Each test binary runs through `go test -exec` with its own action log,
initial working directory, argument list, and completion metadata. Package names
come from directory metadata, never from temporary binary filenames.

The audit forces `-count=1` so every selected test binary executes. This measures
observed access, not cache hits. It preserves test output and Go's test exit status.
Unix interrupts and termination signals are forwarded to the child. Windows
interrupts terminate the child because Go cannot forward `os.Interrupt` there.
Logs aggregate by package, operation, and normalized path; raw paths and event
sequence remain available in JSON. A relative path follows logged `chdir` events.
`getenv` records are discarded; no environment values are included in reports.

The internal `-test.testlogfile` format records `open`, `stat`, and `chdir`.
An open is an **attempt**, including failed and write-only opens. Its result and
read/write mode are absent. Runtime findings therefore use TFS001, evidence
`observed`, and operation names such as `open`; they never claim a confirmed read.
There is no test name, source position, stack, or goroutine attribution. Parallel
tests contribute to a package-level log.

Classification distinguishes checkout/module, external, external/cache-ignored,
and dedicated-temporary observations. Cache-root containment follows the Go 1.27
lexical check and symlink fallbacks, separately from realpath-based display.
Logged `chdir` remains cache-relevant even outside the package root. A checkout
under the system temporary directory remains visible.

By default the existing temporary environment is preserved. `-temp-base` explicitly
creates a fresh owned subdirectory outside selected module roots and sets `TMPDIR`,
`TMP`, and `TEMP` for test processes. The report records this change. The owned
directory is removed after collection. Realpath classification is best effort:
test cleanup may already have deleted files, and symlink or concurrent directory
changes may remain ambiguous.

`-exec`, `-test.testlogfile`, and `-count` overrides are rejected, including in
`GOFLAGS`. Compile-only/list-only invocations, active fuzzing, and benchmark runs
are rejected. Pass custom test flags after `-args`. Paths with spaces are covered
by integration tests, including the executable itself.

| Audit status | Meaning |
| --- | --- |
| 0 | Tests passed and collection completed; observations alone are not failures |
| Go test's nonzero status | Test/build failure takes precedence |
| 2 | Collector, metadata, unsupported-toolchain, missing-log, or report error |
| 3 | Tests and collection passed, but `-fail-on-checkout` found relevant checkout access |

Raw logs and invocation metadata are temporary unless `-keep-logs` is selected.
Missing, malformed, truncated, or interrupted collection never yields a clean
audit. An abnormal test-process exit qualifies even a syntactically complete log.

The audit publishes these blind spots in every JSON report: initialization and
`TestMain` setup before `m.Run`; child-process, syscall, and C-library operations
that bypass the Go hooks; omitted empty/newline-containing names; buffered records
lost on crashes; and absent per-test attribution. A valid log cannot establish
complete coverage. No monkey-patching, Go source modification, internal-package
imports, or cache-layout scraping is used.

## Embedding and metalinters

Import `testfs.Analyzer`, or call `testfs.New()` for independently configurable
flags. The package has no dependency on a particular metalinter. A standard
`multichecker.Main(testfs.Analyzer, ...)` host works directly.

See the [golangci-lint adapter example](examples/golangci/README.md) for its
supported module-plugin mechanism. The host owns `//nolint:testfs` filtering;
standalone `testfs` deliberately reports the raw finding, including intentional
source-contract checks. Put an explained host suppression at the test call site.

## Validation and performance

```sh
go test -count=1 ./...
go test -race -count=1 ./...
go vet ./...
go test -run '^$' -bench BenchmarkAnalyzer -benchmem .
```

Use `-count=1` when editing analysistest fixtures: package loading in subprocesses
is not necessarily represented in Go's test-cache inputs. Fixtures cover safe and
unsafe provenance, aliases, wrappers, helpers/facts, recursion, roots, suppression
ownership, JSON, and read/write distinctions. Disposable-module integration tests
exercise the real vettool and audit, parallel execution, temporary classification,
spaces, failed opens, missing/partial logs, failures, timeouts, and blind spots.

The benchmark generates modules with 10 and 1,000 helper/test pairs, excluding
package loading from timed analysis. Initial measurements and limits are recorded
in [PERFORMANCE.md](PERFORMANCE.md). They are a baseline, not a claim of complete
analysis or a production-scale performance guarantee.

Implementation references: [Go analysis](https://pkg.go.dev/golang.org/x/tools/go/analysis),
[SSA](https://pkg.go.dev/golang.org/x/tools/go/ssa),
[Go test logging](https://go.dev/src/testing/internal/testdeps/deps.go), and
[Go test cache inputs](https://go.dev/src/cmd/go/internal/test/test.go).
