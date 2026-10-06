# Testing refactor plan

## Goals

- Remove the nested `go build` of the testfs CLI from the audit test harness.
- Replace the default work-directory test's live `go test -work` oracle with embedded, captured work-directory fixtures and expected cache-input records.
- Preserve distinct contracts: static findings, observed filesystem operations, Go cache-input selection, and collection failures. None establishes runner-independent cache instability.

## 1. Reuse the test executable for audit integrations

- [ ] Replace `audit/integration_test.go`'s CLI build in `TestMain` with an environment-gated subprocess-helper dispatcher in the existing test executable.
- [ ] Dispatch helper invocations to `audit.Main` and `audit.Launch`; normal invocations must still call `m.Run`. Keep the helper gate explicit so fixture test processes cannot accidentally enter helper mode.
- [ ] Reuse `os.Executable()` for audit invocations. The collector already uses its own executable for `go test -exec`, so the helper must accept its `--testfs-launch` argument shape as well as audit arguments.
- [ ] Preserve executable-path-with-spaces coverage by copying the existing executable to a temporary path with spaces, if necessary; do not rebuild it. Verify this on Windows as well as Unix.
- [ ] Preserve report, output, exit-status, argument-forwarding, timeout, partial/missing-log, and blind-spot assertions.

This removes the redundant CLI compilation, not the real `go test` executions that exercise the audit protocol. A subprocess remains useful for process exits, environment, working directories, output capture, and launcher behavior. Avoid adding an elaborate process-mocking layer merely to remove the build.

## 2. Keep analyzer behavior in analysistest

- [ ] Move the direct/imported-helper assertions from `TestVettoolIntegration` into `analysistest` fixtures, or verify equivalent existing coverage before removing duplication.
- [ ] Keep the existing diagnostic, cross-package fact, filtering, and JSON tests.
- [ ] Explicitly decide whether to retain separate opt-in coverage of the vettool handshake and CLI dispatch. `analysistest` validates the analyzer, not the executable protocol or the custom `-V=full` behavior.

Do not route analyzer assertions through the audit helper. The two components need different test harnesses.

## 3. Embed captured Go work-directory goldens

Here, “work-directory fixtures” means artifacts from `go test -work`, not a `go.work` workspace file.

- [ ] Capture minimal `_testmain.go`, `testlog.txt`, package metadata, and expected Go cache-input operation/path records under `scripts/testdata`. Omit binaries and unrelated build artifacts.
- [ ] Embed these fixtures with `//go:embed` in the tests. Materialize them into temporary directories and pass `-packages-json` so normal fixture tests invoke neither `go test` nor `go list`.
- [ ] Record capture toolchain/platform, fixture scenario, and regeneration instructions. Capture expected inputs from Go's `GODEBUG=gocachehash=1` output, not from testfs itself.
- [ ] Replace machine-specific path prefixes with explicit placeholders during capture. Rebind them consistently in logs, metadata, and expectations during testing; do not normalize away spelling that affects Go's lexical containment behavior.
- [ ] Keep expected records separate from actual testfs output and compare complete sets, not just counts.
- [ ] Cover checkout reads/stats, failed opens, external and temporary files, external `chdir`, spaces, and multiple package identities. Keep platform-specific path fixtures where semantics differ.
- [ ] Recreate any symlink topology required by a scenario when materializing the fixture; embedding file bytes does not preserve symlinks.
- [ ] Retain missing/malformed/truncated-log, missing-metadata, exit-status, and non-mutation tests.
- [ ] Remove the live oracle from the default suite. Provide an explicit developer regeneration/verification command that can run real Go when updating the supported toolchain model.

Captured goldens check conformance to recorded Go behavior. They do not detect a change in the installed toolchain by themselves. Fixture provenance and an explicit refresh procedure preserve the independent oracle without paying for it on every test run.

## 4. Follow-up semantic coverage

These are separate from the harness refactor:

- [ ] Add static regression fixtures for `t.Chdir(t.TempDir())` and `os.Chdir(t.TempDir())`; changing directories must not inherit the ordinary temporary-file exemption.
- [ ] Exercise shared cache-root/path scenarios against both runtime implementations, which currently maintain separate filtering code.
- [ ] If environment analysis is added, test direct and implicit reads separately, including `Getwd`, `TempDir`, and `t.Setenv`'s original-value lookup. Do not expose environment values in fixtures or reports.
- [ ] If cache-instability experiments are added, make them explicitly controlled/opt-in. Include both changed-mtime and deterministic-restored-mtime scenarios; dependency existence is not proof of CI invalidation.

## Acceptance and validation

The default suite must not build a separate testfs executable or run `go test -work` to validate preserved-work inspection. Audit integration tests may still execute real Go tests to validate collection. Existing analysis and report contracts must remain covered, with any intentionally removed executable-protocol coverage documented.

Run focused, uncached tests for the changed harnesses, then the repository's test/race/vet CI matrix on Linux, macOS, and Windows. Use `-count=1` when validating analysistest fixtures: their subprocess package loading is not necessarily represented in the outer test cache inputs.
