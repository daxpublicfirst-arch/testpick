# testpick — Testing

How to verify this tool, what has been verified, with what numbers,
and what is still not verified. Every claim below names the command
that produces it, so it can be re-run.

## Quick start

```sh
go test ./...            # unit tests (cmd + internal/pick)
go vet ./...             # static checks
gofmt -l .               # must print nothing
make build               # ./bin/testpick
./bin/testpick self-test # 5 fixture cases against the real binary
./bin/testpick doctor    # runtime checks
python3 examples/mock-harness.py --binary ./bin/testpick --root .
```

## Test layers

### 1. Unit tests (`go test ./...`)

- `internal/pick/pick_test.go` — 14 tests: diff parsing (basic,
  removed lines, `/dev/null`, malformed hunk, empty diff), selection
  (exact line match, line mismatch, file-level map, name-match-is-not-
  coverage, fail-safe `all`/`none`, reason pluralisation, sorting +
  deduplication), coverage loading errors, `record` append behaviour.
  Coverage: 80.0% of statements in `internal/pick`.
- `cmd/testpick/main_test.go` — 13 tests: flag parsing (values,
  `=` form, missing value, `--` separator), `--request` envelope
  (valid, wrong protocol, missing file), `version` (plain and
  `--json`), `help`, unknown command, no command, `select` end to
  end (stdout + `selection.json` + `selection.txt` agreement),
  unmapped + `none` exit 1, the four error cases with exact codes,
  `record` appending twice, `doctor` (4 checks),
  `--self-test` (builds the real binary, 5 cases),
  `--request` envelope end to end, `compareSelection` semantics,
  `TESTPICK_SELFTEST_RUNNER` command construction.
  Coverage: 74.9% of statements in `cmd/testpick` (was 0.0% before
  these tests existed). The uncovered remainder is mostly
  `main()`/telemetry plumbing and defensive branches.
- `internal/pick/select_bench_test.go` — benchmarks plus a
  large-fixture correctness test (5,000 tests / 200 files → exactly
  500 selected). See "Performance" below.

### 2. Fixture end-to-end (`--self-test`)

`fixtures/basic` and `fixtures/unmapped` each carry a real diff, a
coverage directory, and expected JSON. `--self-test` shells out to
the built binary for five cases (basic × both fail-safe modes,
unmapped × both fail-safe modes, `record`) and compares stdout with
the expected files, then checks `--out` files agree with stdout.
A green self-test means the shipped binary behaves, not just the
library. Fixtures are found next to the binary, in the working
directory, or at `$TESTPICK_FIXTURES`.

### 3. Mock harness (`examples/mock-harness.py`)

The real Arabica harness is not available in this environment, so
the mock plays its part: it calls the CLI the way the plugin contract
says an adapter does and checks every promise in `plugin.toml` and
`schema/` — 17 checks, all passing at last run:

- `plugin.toml` parses; name/version/protocol/commands/permissions
  (network denied) are as specified.
- `version` (plain and `--json`), `doctor` (exit 0, all checks ok),
  `self-test` (exit 0, 5/5 cases).
- `select` via flags and via `--request` envelope; `selection.json`
  matches stdout; `selection.txt` lists the test.
- Determinism: two identical `select` runs give byte-identical stdout.
- Unmapped + fail-safe `all` exits 0 selecting everything;
  unmapped + `none` exits 1 selecting nothing.
- `record` appends a valid JSONL row.
- Missing diff exits 2 with a `NO_DIFF` error object carrying `fix`
  and `if_that_fails`.
- Every run emits exactly one telemetry line on stderr with the
  required fields and `ok` matching the outcome.

What the mock cannot prove: that the real harness's adapter sends
exactly these envelopes. That needs the real harness; see "Not
verified".

### 4. Real-diff end-to-end (manual, repeatable)

Create a small repo, change two lines in `src/a.py`, take a real
`git diff`, build a 3-test coverage map, run `select`. Verified
result: the two tests covering the changed lines are selected, the
test for the untouched file is not, exit 0, `selection.txt` holds
exactly the two selected tests. The same flow with `--fail-safe none`
and with a `--request` envelope gives the same selection. Record
twice into the same `--out` and `missed.jsonl` holds two valid rows.

### 5. Performance (`scripts/gen_perf_input.py`, benchmarks)

Measured on this cloud VM (AMD EPYC, 4 vCPU, Go 1.23.4). Re-run with:

```sh
go test -run=NONE -bench=. -benchtime=3x ./internal/pick/
python3 scripts/gen_perf_input.py --binary ./bin/testpick --out /tmp/perf
```

| Check | Result |
| --- | --- |
| `BenchmarkSelectLarge` (5,000 tests, 200 files, 20 changed) | ~49 ms/op in-process |
| `BenchmarkSelectLargeFailSafeNone` (same) | ~26 ms/op |
| `BenchmarkParseDiffLarge` (10,000 added lines, 100 files) | ~1.6 ms/op |
| End-to-end `select`, 5,000 coverage files on disk | 0.224 s wall, exit 0, 500 selected |
| Oversize input: 205 MiB diff (214,958,193 bytes) | exit 2, `INPUT_TOO_LARGE`, 0.553 s |

All far under the 30 s harness timeout. The end-to-end number is
dominated by reading 5,000 small JSON files, not by selection.

### 6. ARM64 execution (QEMU)

The ARM64 binary is executed for real under `qemu-aarch64-static`
8.2.2 (installed via `apt install qemu-user-static`). Re-run with:

```sh
make test-arm64
```

Verified: `version` prints `testpick 0.1.0` (exit 0); `doctor`
reports `linux/arm64` with 4/4 checks (exit 0); `select` on the
basic fixture selects correctly (exit 0); `self-test` passes 5/5
(exit 0) when run as
`TESTPICK_SELFTEST_RUNNER=qemu-aarch64-static qemu-aarch64-static
./dist/testpick-linux-arm64 self-test`.

Two things about this setup:

- Without the runner env var, `self-test` under QEMU fails with
  `exec format error`, because self-test re-executes the binary and
  the x86_64 host kernel cannot exec an ARM64 binary without
  `binfmt_misc`. The runner var is the supported path (see
  DECISIONS.md D13).
- `binfmt_misc` registration was attempted in this container and is
  **not** usable here: each shell gets a fresh mount namespace, so a
  mount of `/proc/sys/fs/binfmt_misc` does not survive to the next
  shell. On GitHub Actions, `docker/setup-qemu-action` registers it
  for the job and is the right setup there.

QEMU user-mode emulation is not identical to real ARM hardware
(syscall translation, signal handling). It proves the binary runs
and selects correctly on ARM64; it does not prove ARM-specific
performance.

### 7. Release verification

```sh
make release
(cd dist && sha256sum -c SHA256SUMS.txt)   # all 4 lines OK
file dist/testpick-linux-amd64 dist/testpick-linux-arm64
```

Both binaries are `ELF 64-bit LSB executable, statically linked`.
Reproducibility is verified, not assumed: two consecutive
`make release` runs and a rebuild from a fresh clone produce
byte-identical `SHA256SUMS.txt` (see DECISIONS.md D12).

## What is NOT verified

- **Real ARM hardware.** QEMU only (see above).
- **The real Arabica harness.** The mock harness (layer 3) checks
  the contract; only the real harness can check its own adapter.
- **30 s timeout enforcement.** The timeout is harness-side by
  design (DECISIONS.md D15). Nothing in the tool enforces it.
- **Inputs near the limit other than size.** The 205 MiB case checks
  the byte limit. A pathological input under 200 MiB (for example,
  hundreds of thousands of tiny coverage files) has not been timed;
  the harness timeout is the backstop.
- **Concurrent use.** The tool is single-run, single-threaded; two
  simultaneous runs writing to the same `--out` directory have not
  been tested and are not a supported pattern (each run should use
  its own `--out`).
- **Stale coverage maps.** Selection is only as good as its input.
  A test whose coverage changed since the map was recorded is
  invisible to the tool. The fail-safe covers unmapped files, not
  stale maps. The harness should regenerate maps regularly and run
  the full suite on a schedule (see DESIGN.md).
