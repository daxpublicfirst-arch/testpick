# testpick — Architecture

## Repository layout

```text
testpick/
  go.mod                      # module github.com/daxpublicfirst-arch/testpick, go 1.23, no deps
  cmd/testpick/main.go         # CLI: flags, envelope, commands, telemetry, self-test, doctor
  cmd/testpick/main_test.go    # CLI tests (flags, envelope, commands, exit codes)
  internal/pick/
    pick.go                   # Version / ToolName constants
    diff.go                   # Unified-diff parser
    coverage.go               # Coverage-map loader + error type
    select.go                 # Selection algorithm + exit codes
    record.go                 # Missed-test recorder
    pick_test.go              # Unit tests for the core
    select_bench_test.go      # Benchmarks + large-fixture correctness test
  schema/{request,response,error,telemetry}.json
  fixtures/basic, fixtures/unmapped   # End-to-end fixtures + expected JSON
  adapters/coverage_to_testpick.py    # coverage.py / go coverprofile -> small schema
  examples/mock-harness.py    # Mock Arabica harness validating the plugin contract
  scripts/gen_perf_input.py   # Perf input generator + timings (dev only)
  plugin.toml                 # arabica-plugin-v1 contract
  Makefile                    # build / test / self-test / doctor / release / test-arm64
  docs/                       # DESIGN, ARCHITECTURE, DECISIONS, TESTING, RESEARCH, CI
  README.md LICENSE CHANGELOG.md
```

## Data flow

```text
--diff (unified diff) ──┐
                        ├─► ParseDiff ──► Change{file -> {lines}}
--coverage (dir) ───────┤
                        └─► LoadCoverage ──► []CoverageEntry
                                                     │
                              Select(change, coverage, failSafe)
                                                     │
                              ┌──────────────────────┴──────────────────────┐
                              ▼                                             ▼
                     stdout: one JSON object                  --out/selection.json
                     stderr: one telemetry line               --out/selection.txt
```

`record` is a separate, smaller flow: append one JSONL row to
`<out>/missed.jsonl`. It shares nothing with selection.

## Components

### `internal/pick` — the core, no I/O beyond files

The core has no network access, no subprocesses, and no knowledge of
the CLI, the harness, or the plugin protocol. It can be unit-tested
without a binary.

- **`ParseDiff(data []byte) (*Change, error)`** (`diff.go`). Scans
  the diff line by line. `+++ b/<path>` sets the current file
  (`/dev/null` means the file was deleted and contributes nothing);
  `@@ -a,b +c,d @@` sets the new-side line counter; inside a hunk,
  `+` records the current new line as changed, a context line advances
  the counter, `-` advances nothing, `\` (no-newline marker) is
  ignored. A malformed hunk header is the only parse error; a diff
  with no hunks is a valid empty change. Paths keep the `b/` prefix
  stripped and are otherwise used verbatim, so the diff and the
  coverage map must agree on path spelling (both relative to the
  repository root).

- **`LoadCoverage(dir) ([]CoverageEntry, int64, error)`**
  (`coverage.go`). Reads every `*.json` file in the directory, sorted
  by file name, and parses each as one `CoverageEntry`. Returns the
  entries and the total input bytes read. Missing directory or no JSON
  files is `NO_COVERAGE`; malformed JSON or a missing `test`/`files`
  field is `BAD_COVERAGE`. Entries are sorted by test name so
  downstream behaviour does not depend on directory order.

- **`Select(ch, cov, failSafe) Selection`** (`select.go`). The
  algorithm, in order:
  1. For each coverage entry, build a set of its lines. A file-level
     map (no `lines`) selects on file match alone.
  2. For each file the entry names, if the file is changed: mark it
     covered; select the test if the map is file-level or any changed
     line is in the entry's line set.
  3. `unmapped` = changed files no entry named, sorted.
  4. If `unmapped` is non-empty and fail-safe is `all`, replace the
     selection with every test in the coverage directory (sorted,
     deduplicated).
  5. `reason` is built from the outcome: `"<n> changed file(s) has/have
     no coverage"`, `"<n> test(s) selected for <m> changed file(s)"`
     (singular/plural per count), `"no test covers the changed lines"`,
     or `"no changed files"`.
  6. `ExitCode()`: 1 when nothing was selected, or when fail-safe is
     `none` and any file is unmapped; otherwise 0.

  Complexity is linear in the size of the coverage map times the
  changed lines per file. Measured: 5,000 tests / 200 files / 20
  changed files selects in ~49 ms in-process (~0.22 s end to end
  including process start and reading 5,000 JSON files). See
  [TESTING.md](TESTING.md).

- **`Record(outDir, test, now) (RecordRow, string, error)`**
  (`record.go`). Creates the output directory, appends one JSON line
  (`test`, UTC RFC 3339 `at`, `reason: "missed"`) to
  `missed.jsonl`. Appends, never rewrites.

- **`CodedError`** (`coverage.go`). The failure vocabulary: a code,
  a `blocked` description, a `fix`, and an `if_that_fails`. The CLI
  renders it as the failure JSON object.

### `cmd/testpick` — the CLI shell

`main.go` owns argument parsing, the `--request` envelope, command
dispatch, output files, telemetry, `doctor`, and `--self-test`. It
contains no selection logic; it calls `internal/pick`.

- **Flag parsing** is hand-written (`parseFlags`): `--flag value`,
  `--flag=value`, boolean flags (`--json`, `--help`, `--version`),
  and `--` to end flag parsing. There are no third-party flag
  libraries because the flag surface is tiny and stdlib-only is a
  design goal.
- **`run()`** returns `(exitCode, inputBytes, outputBytes, ok)`.
  `main()` prints one telemetry line to stderr and exits with the
  code. Keeping telemetry in `main()` (not in `run()`) is what makes
  `run()` unit-testable: tests capture stdout with a pipe and never
  touch the process exit path.
- **`osExecutable`** is a variable holding `os.Executable`, so tests
  can point `self-test` and `doctor` at a built binary instead of the
  test binary. Without this, `--self-test` could not be unit-tested:
  it re-executes the running binary, and under `go test` that binary
  is the test runner, not the CLI.
- **`--self-test`** shells out to the built binary for every fixture
  case (both fail-safe modes plus `record`) and compares stdout
  against the expected JSON, then checks that `--out` files exist and
  agree with stdout. It deliberately tests the real binary end to
  end, not the internal functions — a green self-test means the
  shipped binary behaves, not just the library.
- **`TESTPICK_SELFTEST_RUNNER`** (env): if set, self-test runs each
  case as `<runner> <binary> <args...>`. This exists for
  cross-architecture testing: `qemu-aarch64-static` on an x86_64 host
  without `binfmt_misc` cannot exec an ARM64 binary directly, so the
  runner prefix is how `make test-arm64` works. See
  [TESTING.md](TESTING.md).
- **Fixture discovery** (`findFixtures`): `$TESTPICK_FIXTURES` first,
  then next to the binary, then the working directory. This is what
  lets an installed binary (no checkout around it) still self-test
  when given a fixtures directory.

## Determinism

- `selected` and `unmapped` are sorted and deduplicated.
- Coverage entries are sorted by test name at load time.
- No map iteration order leaks into output: every output list is
  built from a sorted slice.
- `record` timestamps are the only non-deterministic output, and
  `record` output is a log, not a selection.
- Verified: `examples/mock-harness.py` runs `select` twice and
  requires byte-identical stdout.

## Concurrency

None. The tool is single-threaded: read inputs, select, write
outputs, exit. There is no shared state, no daemon, no locking. The
200 MiB input limit bounds memory: the diff is read whole into memory
(`os.ReadFile`) and the coverage map is held as parsed structs. The
largest verified input is a 205 MiB diff, rejected with
`INPUT_TOO_LARGE` in 0.55 s without parsing it.

## Failure handling

Failures are values, not panics. Every fallible step returns a
`CodedError` with a remediation, and the CLI prints exactly one JSON
object and exits non-zero. There are two exit-code families: 2 for
"the input or the invocation is wrong" (the caller can fix it) and 3
for "the tool itself failed" (cannot write output, self-test mismatch,
doctor check failed). Exit 1 is not a failure at all: it means "no
test covers the change", and the caller decides what to do.

## Release builds

`make release` builds static binaries (`CGO_ENABLED=0`,
`-trimpath`, `-ldflags "-s -w"`, `-buildvcs=false`) for
linux-amd64 and linux-arm64, packs each into a tarball with
`plugin.toml`, `README.md`, `LICENSE`, `CHANGELOG.md`, and writes
`SHA256SUMS.txt`. The tarballs are deterministic (sorted entries,
fixed mtime/owner/group, `GZIP=-n`), so rebuilding from the same code
produces identical hashes — verified from a fresh clone. See
[DECISIONS.md](DECISIONS.md) for why.
