# Go test filesystem dependency analyzer and runtime audit

Status: implementation proposal. Written 2026-09-18.
Working name: `testfs`. Names below describe proposed interfaces, not existing commands.

## Purpose and decision

Build a reusable Go tool that identifies tests whose results depend on runtime filesystem access. Its main use is preventing needless test-result cache misses after a fresh checkout. It must work across repositories without project-specific fixture paths or function bans.

Ship two independent entry points:

- A `go/analysis` analyzer for source diagnostics and metalinter integration.
- A runtime audit that wraps `go test` and reports observed filesystem dependencies, including accesses inside libraries.

Implement the analyzer as the stable enforcement surface. Prototype the runtime collector first to test feasibility and expose indirect accesses. The two modes share a finding schema, but report different evidence. Static findings mean an operation may access disk. Runtime findings mean an operation was observed. Neither alone proves an avoidable cache miss.

Intentional source and cross-language checks should produce diagnostics. Users can suppress them in their metalinter, with a reason. Embedded filesystems and proven temporary paths should be allowed by default.

This proposal does not change any application's linter configuration. It does not require modifying Go, importing internal standard-library packages, or patching application tests.

## Problem model

Go's test-result cache records certain runtime file operations and checks filesystem metadata on subsequent runs. A checkout can change that metadata without changing file contents. Directory observations matter too. Files outside the package's module/root are treated differently, so a disk access is not automatically a cache invalidation. See [Go's cache input computation](https://go.dev/src/cmd/go/internal/test/test.go).

Embedding turns fixture contents into build inputs. It avoids the corresponding runtime disk read, while retaining invalidation when the content changes. An embedded fixture can also be written into a temporary directory before exercising production filesystem code. See the [embed package](https://pkg.go.dev/embed).

The tool must distinguish these cases:

| Case | Default treatment |
| --- | --- |
| Read a checked-out fixture or source file | Report |
| Inspect a checkout directory, glob it, or stat a file | Report |
| Read through `embed.FS`, including an interface conversion | Allow |
| Read a file created beneath `t.TempDir()` | Allow when path provenance is retained |
| Copy embedded bytes to a temporary file, then open it | Allow |
| Copy a disk fixture to a temporary file | Report the original fixture read |
| Read through `os.DirFS("testdata")` | Report |
| Read through an unresolved `fs.FS` implementation | Report uncertainty, not a proven disk read |
| Intentional source-contract test | Report; permit an explained host-linter suppression |

## Deliverables and scope

Publish an optional `testfs/analyzer` package and contained command-line driver. The analyzer name is `testfs`. Keep the reusable analysis package independent of a specific metalinter version. Runtime collection and preserved-work inspection are documented at the repository root.

Provide a `singlechecker` executable usable directly or through `go vet -vettool`. Document integration into a metalinter using its supported analyzer/plugin mechanism. Host linters own `//nolint:testfs` processing; the analyzer must not silently interpret that comment differently. A standalone runner may offer explicit diagnostic filtering, but must document its own syntax.

Use stable rule identifiers:

- `TFS001`: filesystem access through a disk-backed API or filesystem.
- `TFS002`: filesystem access whose backing store or path safety could not be resolved.
- `TFS003`: a disk write/open that can add a runtime filesystem dependency.

Default static output includes all three, with confidence and operation type. A strictness option may filter uncertain findings. No automatic suppression should depend on a package or test name containing `drift`, `contract`, or `integration`.

Non-goals for the first release: complete whole-program pointer analysis, syscall tracing, automatic fixture relocation, test sandboxing, or guaranteed proof that a suite is hermetic. Benchmarks and active fuzz campaigns can follow after ordinary tests and seed execution work correctly.

## Static analysis design

Use syntax and type information from `go/analysis`, with SSA where value flow requires it. Resolve functions by package path and symbol identity. An import alias or another package's method named `ReadFile` must not change the result. The framework supports reusable analyzers and facts; SSA supplies a representation for value-flow analysis. See [go/analysis](https://pkg.go.dev/golang.org/x/tools/go/analysis) and [go/ssa](https://pkg.go.dev/golang.org/x/tools/go/ssa).

### Roots and operations

Analyze test functions, examples, `TestMain`, test-file initializers, and helpers reachable from those roots. Include internal and external test packages without duplicate diagnostics. Do not report ordinary production filesystem code merely because its package has tests.

Start with these operation families:

- `os.ReadFile`, `Open`, `OpenFile`, `Stat`, `Lstat`, `ReadDir`, and directory traversal through `filepath.Glob` or `Walk`/`WalkDir`.
- `fs.ReadFile`, `ReadDir`, `Stat`, `Glob`, `WalkDir`, and `fs.FS.Open`, classified using the filesystem receiver's provenance.
- Standard-library consumers such as `template.ParseFiles`, `ParseGlob`, and `ParseFS`, using explicit models.
- `os.DirFS`, `fs.Sub`, and supported rooted filesystem APIs as provenance constructors or transformers.
- `os.WriteFile` and write-mode opens as separate operations. A write must not be mislabeled as a read.

Model methods on file handles where useful, but report their originating open once rather than every `Read`. Do not diagnose creation of an `os.DirFS` value by itself unless it is actually consumed.

Keep API models versioned and covered by tests. Unknown library calls are not automatically filesystem sinks. Report an indirect operation only when a summary or resolved callee provides evidence.

### Value provenance

Represent possible origins as a set. Useful origins include embedded filesystem, in-memory filesystem, temporary root, disk root, parameter-dependent value, and unknown. Preserve the distinction between a filesystem value and a path string.

Track assignments, interface conversions, tuple returns, closures, and control-flow merges. At a merge, retain all possible origins. A temporary path on one branch and a fixture path on another must not become safe.

For embedded filesystems:

- Recognize `embed.FS` by type identity, including imported values and aliases.
- Preserve its origin through conversion to `fs.FS`, known wrappers, and `fs.Sub`.
- Recognize `testing/fstest.MapFS` as in-memory. If constructing it reads a disk file, report that earlier read.
- Treat an unresolved custom filesystem as unknown. Do not classify every `fs.FS` as disk-backed.
- Derive wrapper summaries where the implementation is available. Avoid a global user-maintained allowlist of type names.

The `fs.FS` interface alone does not identify its backing store. `os.DirFS` supplies disk access, and path/symlink behavior needs care. See [io/fs](https://pkg.go.dev/io/fs) and [os.DirFS](https://pkg.go.dev/os#DirFS).

For temporary paths:

- Recognize actual `testing.T`, `testing.B`, and `testing.TB` `TempDir` methods, not an arbitrary method with that spelling.
- Recognize `os.MkdirTemp` and `os.CreateTemp` when their parent is proven temporary or the default system temporary directory is selected. A parent under the checkout is not automatically safe.
- Preserve origin through `filepath.Join`, safe constant concatenation, local helper returns, and known handle operations.
- Require appended components to remain beneath the temporary root. Constant `..` escapes lose safety. Dynamic components require a supported proof, such as a validated local path; otherwise report uncertainty.
- Do not consider `os.TempDir()` alone proof that a pre-existing file is test-owned.
- Lose or qualify the proof when known symlink creation, path mutation, or an unresolved wrapper can escape the root.

A temporary directory is an intended safe testing pattern, not a security boundary or an unconditional caching guarantee. An unusual `TMPDIR` inside the module can still affect caching. State this limitation. See [testing.TempDir](https://pkg.go.dev/testing#T.TempDir).

### Helpers and imported code

Analyze local helpers with parameterized summaries. For example, `read(path)` records that it opens its argument; instantiate the summary at each test call site. This permits `read(filepath.Join(t.TempDir(), "db"))` while reporting `read("testdata/db")`.

For imported helpers, export versioned `analysis.Fact` summaries where practical. Facts describe parameter-to-operation and parameter-to-return relationships, not a single unsafe/safe bit. Compute local recursive summaries to a bounded fixed point. When a budget is exceeded, retain uncertainty instead of silently marking the call safe.

Stage the implementation: local flow and same-package helpers first, imported summaries second. Document the indirect calls missed before the second stage. Do not promise arbitrary reflection, unsafe operations, or interface dispatch resolution.

Report at the test-side call when possible, with related locations for the helper's disk operation. This gives metalinter suppressions a useful location. A production helper should not require a suppression that hides all its callers.

Example diagnostic:

```text
attachment_test.go:42: TFS001: reading "testdata/workbook.xlsx" at runtime may make cached test results depend on checkout metadata; embed the fixture or use a temporary copy of embedded data (testfs)
```

Do not autofix. Embedding may cross a module boundary, change intentional source checks, or require a different fixture owner.

## Runtime audit: a practical go test wrapper

Yes, Go exposes a usable implementation mechanism. Test binaries have an internal `-test.testlogfile` flag. The test dependency logger writes `open`, `stat`, `chdir`, and `getenv` records. This is an internal interface, not a supported public tracing API. See [testing's flag and lifecycle](https://go.dev/src/testing/testing.go) and the [test dependency logger](https://go.dev/src/testing/internal/testdeps/deps.go).

Use a small launcher through the public `go test -exec` option. Go retains ownership of package selection, compilation, working directories, and test output. The launcher starts each test binary with a unique action-log path and collects the result. The `-exec` facility is documented in the [go command](https://pkg.go.dev/cmd/go). Current Go code omits its normal automatic action-log argument when an execution wrapper is present; the launcher must explicitly supply its own flag.

Proposed user interface:

```sh
testfs audit ./...
testfs audit -json audit.json -- -race -run TestAttachment ./...
```

These commands are a design, not a validated prototype.

### Collector contract

1. Resolve the selected Go toolchain and package metadata. Record the Go version, platform, build flags, and package directories.
2. Start `go test` with the launcher and `-count=1` so cached results cannot hide executions. Explain that this is an audit run, not a cache-hit measurement.
3. Give each launcher invocation its own log and metadata file. Record its working directory and arguments. Preserve stdout, stderr, signals, timeout behavior, and the test process's status.
4. Inject the log flag before a possible argument separator. Reject conflicting user-supplied log flags and unsupported existing `-exec` wrappers rather than overriding them silently. Test paths containing spaces on all supported platforms.
5. Parse records by splitting at the first space. Retain sequence and initial working directory; update path resolution on logged `chdir` events. Validate the header and distinguish missing, partial, and malformed logs.
6. Aggregate by package, operation, and normalized path. Use package-directory metadata to identify packages, never a guessed binary basename. Keep raw and resolved paths where resolution is ambiguous.
7. Report checkout/module observations separately from external filesystem observations and temporary access. Keep Go-cache root filtering distinct from realpath-based display classification.
8. Preserve test failure as the primary outcome. Return a separate documented status for audit failures and policy findings. A missing collector log must never be reported as a clean audit.

Version-gate the internal-log adapter. Test each supported Go release on Linux, macOS, and Windows. For unsupported releases, emit a clear collector limitation; do not fall back to a success report. Avoid relying on Go's build-cache file layout or scraping `GODEBUG` output as the primary data API.

### Embedded and temporary access at runtime

Embedded reads naturally produce no underlying disk-open record for the fixture. Library calls that eventually use instrumented `os` APIs appear without needing a library-specific model.

The log does not identify `t.TempDir()` ownership. Do not infer it solely because a path is somewhere beneath `/tmp`; a checkout may itself be there. By default classify paths outside cache-relevant module roots as external/cache-ignored where that matches the selected Go version, not as proven temporary.

Offer an explicit audit option to provide a dedicated temporary base outside the workspace and set the platform's temporary-directory environment variables for the test process. Paths beneath that base can be classified separately, subject to symlink caveats. This option changes test environment and must be recorded in the report. Never silently reset a project's existing temporary-directory configuration.

### Limits and precise terminology

An `open` record means an attempted open, not proof that bytes were read. It has no result or mode. `os.OpenFile` logs before opening and also logs write-mode opens. See [os.OpenFile implementation](https://go.dev/src/os/file.go). Runtime output must say “filesystem access” or “open observed,” not “read confirmed.”

The internal log also lacks source positions, call stacks, goroutine identity, and test names. Do not assign a file operation to whichever parallel test most recently emitted a JSON event. Report package-level evidence. A user-selected rerun of one test can narrow attribution, but helpers and background goroutines still participate.

Logging starts during `m.Run`; it does not cover package initialization or `TestMain` setup before that call. It misses child-process I/O, direct syscalls, and C-library accesses that bypass the hooks. A crash may leave buffered records unwritten. Current logging also omits empty names and names containing newlines. The audit must publish these coverage limits.

A wrapper cannot replace `os.ReadFile` globally using a Go module replacement. Avoid monkey-patching, `go:linkname`, and custom toolchains. Optional syscall tracing could later cover processes and earlier startup on selected platforms, but it brings OS-specific privileges, noise, and attribution work.

## Evidence and reporting

Both modes produce versioned JSON with package, rule, operation, evidence kind, confidence, reason, path or expression, and source location when known. Runtime results may have no source location. Include scope/coverage limitations and suppression status. Do not expose environment values; this tool only needs filesystem findings.

Separate three statements in reports:

- Potential dependency: static analysis found a relevant operation.
- Observed dependency: the audit recorded an operation during this run.
- Reproduced cache miss: a controlled cache experiment reran after only metadata changed.

The first release needs the first two. An optional future verification command can warm the cache and perturb fixture timestamps in a disposable workspace. Keep its absolute path, environment, flags, contents, and cache stable across phases. Do not use `-count=1` in the measurement phases. Never alter the user's live checkout for this experiment. A warm baseline must actually hit before the result can be attributed to metadata changes.

## Acceptance tests

Use `analysistest` fixtures with expected diagnostics, plus small standalone modules for runtime integration. Avoid application-specific dependencies. Test at least:

| Test case | Required result |
| --- | --- |
| Aliased `os.ReadFile` of a literal fixture | `TFS001` |
| `Stat`, directory enumeration, and glob under checkout | Report metadata/directory operation |
| Direct `embed.FS.ReadFile` | No diagnostic |
| Embedded FS converted to `fs.FS`, then `fs.ReadFile` | No diagnostic |
| Embedded FS passed through a local helper or `fs.Sub` | No diagnostic |
| `fstest.MapFS` with literal data | No diagnostic |
| MapFS populated by a disk read | Report source read |
| `os.DirFS("testdata")` passed through `fs.Sub` and read | Report |
| `t.TempDir` plus a contained filename, then open/stat | No diagnostic |
| Embedded data written to a temp file, then library open | No diagnostic when helper summary is available |
| Test-owned temporary parent through a local helper | No diagnostic |
| Temp path mixed with checkout path at a branch | Report |
| Escaping or unresolved appended path | Report risk or uncertainty |
| Source-contract check with metalinter suppression | Raw analyzer finding; host filters it |
| Arbitrary `fs.FS` receiver | `TFS002`, not a definite disk claim |
| Production-only disk operation with no test reachability | No test diagnostic |
| Library reads via `os` while a test runs | Runtime access record |
| Embedded fixture read | No runtime fixture access record |
| Temp base and checkout both under system temp | Checkout access remains visible |
| Parallel packages and tests | Separate package logs; no invented test attribution |
| Write-only open, failed open, path containing spaces | Correct access terminology and parsing |
| Relative path after chdir | Correct resolved path or explicit uncertainty |
| Init, pre-m.Run, subprocess, and direct-syscall accesses | Documented blind spots exercised by tests |
| Failed or timed-out test, missing/truncated log | Failure/partial coverage, never clean success |
| Changed fixture bytes | Build/test behavior remains outside the analyzer's control |

Set performance budgets after an initial benchmark across small and large modules. Record analysis overhead, peak memory, and unresolved-call counts. Expose bounded analysis failures as coverage information. Correct handling of embedded and temporary inputs takes precedence over claiming complete coverage.

## Implementation sequence

1. Build a disposable runtime prototype around `-exec` and `-test.testlogfile`. Validate argument handling, per-package attribution, platform behavior, and the documented blind spots. This decides whether the internal-log backend is maintainable.
2. Publish the analyzer with type-resolved operations, embedded/in-memory provenance, temporary-path tracking, and same-package helper summaries. Deliver the standalone command and metalinter integration example.
3. Add imported helper facts, broader standard-library models, and stable runtime JSON. Compare static findings with audit observations to prioritize missed patterns.
4. Consider a controlled cache-verification command and optional platform tracing only after usage demonstrates a need.

The initial release is useful without complete program analysis: it catches direct regressions, permits common safe fixtures, and gives users a separate way to discover indirect dependencies.

## Verification basis for this proposal

The logging mechanism and lifecycle were inspected in the local Go 1.27.0 source, including `testing/testing.go`, `testing/internal/testdeps/deps.go`, `os/file.go`, and `cmd/go/internal/test/test.go`. Public source and API references are linked above. No runtime prototype or analyzer has been implemented or executed for this spec. Internal API compatibility remains a required feasibility test, not an assumed guarantee.
