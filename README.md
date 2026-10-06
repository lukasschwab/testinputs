# testfs

`testfs` analyzes preserved Go test work directories to identify filesystem and
environment inputs that can affect test-result caching.

These checks are especially relevant to CI: the Go cache's hash representation
of files read by tests includes the file's modification timestamp, which often
(as in GitHub Actions) reflects when the repository was clined rather than when
the file was last modified. This kind of read results in test packages running
every time in CI, even though the cache should be fresh.

These issues are commonly resolved with `//go:embed` directives and `testing`
utilities like `t.TempDir`.

## Installation

```bash
go install github.com/lukasschwab/testfs/cmd/testfs@latest
```

## Usage

In your Go module root:

```bash
go list -json ./... > packages.json
go test -work ./...
# Go prints WORK=/path/to/work
testfs -work /path/to/work -packages-json packages.json
```

## Findings

| Output label | What it represents | Common fix |
|:---|:-----|:-----|
| `open "/path/to/file"` | An attempted file open, including failed or write-only opens. In-root file metadata can affect caching. | Embed fixtures; write embedded data to `t.TempDir()` if disk access is required. Keep generated files outside the checkout. |
| `open "/path/to/directory"` | A directory open; Go hashes its entries’ metadata. The log itself does not distinguish file opens from directory opens. | Avoid scanning checkout directories; use embedded data or explicit inputs. |
| `stat "/path"` | A file or directory metadata/existence check within Go’s tracked root. | Remove unnecessary checks; use embedded fixtures or test-owned temporary files. |
| `chdir "/path"` | A working-directory change. Go tracks the path and directory metadata even outside the checkout. | Prefer explicit paths over changing directories. Changing into `t.TempDir()` is not exempt. |
| `getenv "environment NAME"` | A logged environment read, including reads performed indirectly by library APIs. | Avoid unnecessary ambient reads; inject configuration into the code under test, or keep the variable stable in CI. |
| `getenv "environment GODEBUG"` with implicit evidence | Go’s implicit cache dependency, even without a logged read. JSON labels it `evidence: "implicit"`; a logged read is reported separately. | Keep `GODEBUG` stable across CI runs; this dependency generally cannot be removed from test code. |

## Optional static analyzer

The source analyzer is intentionally contained in [`analyzer`](analyzer/). It is
not part of the default `testfs` command or runtime inspection dependency graph.
See its [README](analyzer/README.md) to build the optional vettool, embed
`testfs/analyzer`, or use the golangci-lint adapter example.

