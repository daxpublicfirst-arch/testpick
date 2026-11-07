# testpick

Select the tests that reach a change, from a diff and a per-test coverage
map. A full test suite is slow; a change usually touches a small part of
the code. testpick returns the tests that cover the changed files and
lines, and records a missed test so the selection can be corrected later.

It is a standalone CLI (Go, one static binary). It reads local files only:
no network, no model calls, no daemon. A harness may wrap it with a thin
adapter; the tool itself knows nothing about any harness. See
`plugin.toml` (`protocol = "arabica-plugin-v1"`).

## Docs

- [docs/DESIGN.md](docs/DESIGN.md) — what testpick is for, who uses it, inputs/outputs, and what it deliberately does not do.
- [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) — layout, data flow, components, determinism, failure handling.
- [docs/DECISIONS.md](docs/DECISIONS.md) — decision log: every choice, the alternatives, and the trade-off.
- [docs/RESEARCH.md](docs/RESEARCH.md) — prior art (regression test selection, testmon, vitest, QEMU) and what it changed here.
- [docs/TESTING.md](docs/TESTING.md) — how to verify the tool, measured numbers, and what is still not verified.
- [docs/CI.md](docs/CI.md) — the CI workflow, and why it is not on `main` yet (the GitHub App cannot push `.github/workflows/`).

## Verification

Verified from clean clones with a fresh Go 1.23.4 toolchain
(`GOPROXY=off`, empty caches). Full detail in
[docs/TESTING.md](docs/TESTING.md); the short version:

- `go test ./...`: 14 unit tests in `internal/pick` (80.0% coverage)
  and 13 in `cmd/testpick` (74.9% coverage) — all pass.
- `--self-test`: 5/5 fixture cases. `doctor`: 4/4 checks.
- End-to-end with a real `git diff`: selects exactly the tests
  covering the changed lines; error cases exit 2 with the documented
  codes; empty diff and unmapped + `--fail-safe none` exit 1.
- **Performance** (5,000 tests / 200 files / 20 changed files):
  `select` takes ~0.22 s end to end; a 205 MiB diff is rejected with
  `INPUT_TOO_LARGE` in 0.55 s. Both far under the 30 s harness limit.
  Run it yourself: `python3 scripts/gen_perf_input.py --binary ./bin/testpick`.
- **ARM64 runs for real** under `qemu-aarch64-static` 8.2.2
  (`make test-arm64`): `version`, `doctor` (reports `linux/arm64`),
  `select`, and `self-test` (5/5) all pass.
- **Mock harness** (`python3 examples/mock-harness.py`): 17/17
  checks pass — it calls the CLI the way an adapter would and
  validates every promise in `plugin.toml` and `schema/`.

Not verified: real ARM hardware (QEMU only), the real Arabica harness
(the mock checks the contract), and 30 s timeout enforcement (that is
harness-side by design).

## Example

```sh
# A diff touching src/a.py lines 10-12, and a coverage map in coverage/:
testpick select --diff change.patch --coverage coverage/ --out ./out --json
```

```json
{"ok":true,"tool":"testpick","version":"0.1.0",
 "selected":["tests/test_a.py::test_x"],
 "unmapped":[],
 "reason":"1 test selected for 1 changed file",
 "fail_safe":"all"}
```

`--out` also writes `selection.json` (the same object) and
`selection.txt` (one test per line). Try it with the shipped fixture:

```sh
make build
./bin/testpick select --diff fixtures/basic/change.patch \
  --coverage fixtures/basic/coverage --out ./out --json
cat out/selection.txt
```

## Install

Requires Go 1.23+ to build from source; the released binaries need
nothing else.

```sh
make build          # ./bin/testpick
make install        # /usr/local/bin/testpick
```

Release tarballs (linux-amd64, linux-arm64) are built with `make
release`; verify them with `SHA256SUMS.txt`. Release builds are
reproducible: binaries are built with `-buildvcs=false` (no VCS stamp)
and tarballs use sorted entries with a fixed timestamp, so rebuilding
from the same code produces identical hashes.

## Commands

```text
testpick select --diff <patch> --coverage <dir> --out <dir> [--fail-safe all|none] [--json]
testpick record --missed <test> --out <dir> [--json]
testpick self-test [--json]
testpick doctor [--json]
testpick version [--json]
testpick --request <envelope.json>
```

Every command writes one JSON object to stdout and one telemetry line
to stderr (see below). `--version` prints `testpick 0.1.0` unless
`--json` is given.

### `select`

`--diff` is a unified diff (`git diff`, `git diff --no-index`,
`diff -u`). Changed lines come from `+++ b/<path>` and hunk headers
`@@ -a,b +c,d @@`; each added line is a changed line. Removed lines are
ignored.

`--coverage` is a directory of JSON files, one per test:

```json
{"test":"tests/test_a.py::test_x",
 "files":["src/a.py"],
 "lines":[10,11,12]}
```

A test is selected when it covers a changed file **and** a changed line,
or when its coverage names the file with no line detail (a file-level
map). A test is never selected just because its name resembles a changed
file; coverage is required.

`unmapped` lists changed files that no test covers. If it is non-empty
and `--fail-safe all` (the default), `selected` includes every test in
the coverage directory and `reason` says why. With `--fail-safe none`,
`selected` stays the exact match and the caller decides.

`selected` is sorted and deduplicated. Inputs are limited to 200 MiB
in total (`limits.input_mb` in `plugin.toml`).

### `record`

```sh
testpick record --missed tests/test_a.py::test_y --out ./out --json
```

Appends one JSONL row to `<out>/missed.jsonl`:

```json
{"test":"tests/test_a.py::test_y","at":"2026-11-08T00:00:00Z","reason":"missed"}
```

This is a record for later analysis; it does not change selection.

### `self-test`, `doctor`, `version`

- `--self-test` runs every fixture in `fixtures/` (both fail-safe modes
  plus `record`) and exits 0 only when every expected output matches.
  It finds the fixtures next to the binary, in the working directory,
  or at `$TESTPICK_FIXTURES`.
- `doctor` checks the runtime (Linux, temp directory writable,
  executable, working directory) and exits 0 only when the tool can run.
- `--version` prints `testpick 0.1.0`.

### `--request` envelope

An adapter may pass a JSON envelope instead of flags:

```json
{"protocol":"arabica-plugin-v1","command":"select",
 "args":{"diff":"/abs/change.patch","coverage":"/abs/coverage","out":"/abs/out"}}
```

```sh
testpick --request request.json
```

Flags on the command line win over envelope values. The CLI still
works with flags alone.

## Exit codes

| Code | Meaning |
| ---- | ------- |
| 0 | Success. |
| 1 | No test covers the change. Not an error; the caller decides. This includes fail-safe `none` with an unmapped file, even if another change matched exactly. |
| 2 | Usage or input error (missing/unreadable diff or coverage, malformed diff/coverage JSON, bad flags). |
| 3 | Tool failure (cannot write `--out`, `--self-test` mismatch, `doctor` check failed). |

## Failure output

On failure the tool prints exactly one JSON object to stdout and exits
non-zero:

```json
{"ok":false,"error":{"code":"NO_COVERAGE","blocked":"...","fix":"...","if_that_fails":"..."}}
```

Error codes: `NO_DIFF`, `BAD_DIFF`, `NO_COVERAGE`, `BAD_COVERAGE`,
`BAD_REQUEST`, `INPUT_TOO_LARGE`, `IO_ERROR`, `NO_FIXTURES`,
`TOOL_ERROR`. Schemas live in `schema/` (`request`, `response`,
`error`, `telemetry`).

## Telemetry

One JSON line to stderr per run. It carries sizes and the outcome
only, never file contents:

```json
{"tool":"testpick","version":"0.1.0","command":"select",
 "duration_ms":123,"input_bytes":4567,"output_bytes":890,"ok":true}
```

## Coverage adapters

The core reads only the small schema above. Language tools that
already collect coverage can be converted with
`adapters/coverage_to_testpick.py` (stdlib only, no network):

```sh
# coverage.py JSON (run coverage once per test to get a per-test map)
python3 adapters/coverage_to_testpick.py coveragepy coverage.json \
  --test tests/test_a.py::test_x --out coverage/

# go coverprofile (run per package/test for a per-test map)
python3 adapters/coverage_to_testpick.py gocover cover.out \
  --test pkg/foo.TestX --out coverage/
```

## Assumptions and decisions

- **Multi-file coverage maps:** in `{"files": [...], "lines": [...]}`
  the lines apply to every listed file. A map needing different lines
  per file should be split into one entry per file.
- **Empty diff:** selects nothing, reason `no changed files`, exit 1.
- **Fail-safe `all` with an unmapped file** selects every test and
  exits 0 (a conservative success). Fail-safe `none` with an unmapped
  file exits 1.
- **Timestamps in `record`** are UTC RFC 3339.
- **No coverage collection:** the tool never runs a test runner or
  collects coverage itself; it reads the map.
- **No writes outside `--out`:** `select` and `record` write only
  under `--out`.

## Development

```sh
go test ./...        # unit tests
go vet ./...         # vet
gofmt -l .           # formatting (must be empty)
make self-test       # fixture end-to-end checks
make doctor          # runtime checks
make release         # static linux-amd64/arm64 binaries + tarballs + SHA256SUMS.txt
make test-arm64      # run the ARM64 binary under qemu-aarch64-static
python3 examples/mock-harness.py --binary ./bin/testpick --root .
python3 scripts/gen_perf_input.py --binary ./bin/testpick --out /tmp/perf
```

CI (`.github/workflows/ci.yml`) runs `go test`, `--self-test`, and
`doctor` on every push to `main`. The workflow file itself is not on
`main` yet — the GitHub App cannot push `.github/workflows/` (403, no
`workflows` permission) — so a copy is kept at `docs/ci.yml` with
install instructions in [docs/CI.md](docs/CI.md).

## License

MIT. See `LICENSE`.
