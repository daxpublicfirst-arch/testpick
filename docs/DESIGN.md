# testpick — Design

This document explains what testpick is for, who uses it, what it takes
in, what it gives back, and what it deliberately does not do. It is the
starting point for anyone reading this repository. The decision log
lives in [DECISIONS.md](DECISIONS.md); the research behind it lives in
[RESEARCH.md](RESEARCH.md).

## The problem

A full test suite is slow. In a large repository it can take minutes
or hours, and most of that time is spent re-running tests that cannot
possibly be affected by the change under review. A change usually
touches a small part of the code. If we know, for each test, which
files and lines it reaches, we can run only the tests that reach the
change — and run the full suite less often, for example on a schedule
or before a release.

testpick is the small piece that answers one question: **given this
diff and this per-test coverage map, which tests should run?** It does
not run tests, does not collect coverage, and does not decide what
"the suite" is. It answers the question and gets out of the way.

## Who uses it

The direct user is an agent harness (the "Arabica" harness in
`plugin.toml`). The harness wraps testpick with a thin adapter: it
calls the CLI with a diff and a coverage directory and reads JSON.
testpick must not know the harness exists — no harness paths, ticket
ids, product names, or environment variables in code, tests, docs, or
fixtures. A human can also run it directly; the README shows how.

## Inputs

Two inputs, both local files. No network, no model calls, no API keys.

1. **`--diff` — a unified diff.** As produced by `git diff`,
   `git diff --no-index`, or `diff -u`. testpick reads the `+++ b/<path>`
   lines for changed paths and the `@@ -a,b +c,d @@` hunk headers for
   changed lines. Each added line (a `+` line inside a hunk) is a
   changed line. Removed lines are ignored: a deleted line cannot be
   "reached" by a test in the new code.

2. **`--coverage` — a directory of JSON files, one per test.** Each
   file is one small object:

   ```json
   {"test": "tests/test_a.py::test_x",
    "files": ["src/a.py"],
    "lines": [10, 11, 12]}
   ```

   The schema is intentionally small. A language adapter converts
   whatever a coverage tool already produces — `coverage.py`,
   `vitest --coverage`, `go test -coverprofile`, or a custom map —
   into this shape; see `adapters/coverage_to_testpick.py`. The core
   tool reads only this shape. A map with no `lines` is file-level:
   the test is taken to cover the whole of every listed file.

## Outputs

`select` writes one JSON object to stdout and the same object to
`<out>/selection.json`, plus `<out>/selection.txt` (one test per line):

```json
{"ok": true, "tool": "testpick", "version": "0.1.0",
 "selected": ["tests/test_a.py::test_x"],
 "unmapped": ["src/other.py"],
 "reason": "1 changed file has no coverage",
 "fail_safe": "all"}
```

Selection rules (see [ARCHITECTURE.md](ARCHITECTURE.md) for the
algorithm):

- A test is selected when it covers a changed file **and** a changed
  line, or when its coverage names the file with no line detail.
- A test is never selected merely because its name resembles a changed
  file. Coverage is required. Name matching is how false confidence
  gets built.
- `unmapped` lists changed files that no test covers at all.
- If `unmapped` is non-empty and `--fail-safe all` (the default),
  `selected` includes **every** test in the coverage directory. This
  is the safe fallback: when the tool cannot see part of the change,
  it refuses to silently skip it and runs everything instead. With
  `--fail-safe none`, `selected` stays the exact match and the caller
  decides what to do.
- `selected` is sorted and deduplicated, so the same input always
  produces the same output.

`record` appends one JSONL row to `<out>/missed.jsonl`:

```json
{"test": "tests/test_a.py::test_y", "at": "2026-11-08T00:00:00Z", "reason": "missed"}
```

Recording a missed test is a record for later analysis. It never
changes selection.

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
to stderr. `--request` accepts an adapter envelope
(`{"protocol": "arabica-plugin-v1", "command": "select", "args": {...}}`)
as an alternative to flags; flags on the command line win over
envelope values. The CLI still works with flags alone.

`--self-test` runs every fixture in `fixtures/` (both fail-safe modes
plus `record`) and exits 0 only when every expected output matches.
`doctor` checks the runtime (Linux, temp directory writable,
executable, working directory) and exits 0 only when the tool can run.
`--version` prints `testpick 0.1.0`.

## Exit codes and failure model

| Code | Meaning |
| ---- | ------- |
| 0 | Success. |
| 1 | No test covers the change. Not an error; the caller decides. This includes fail-safe `none` with an unmapped file, even if another change matched exactly, and an empty diff. |
| 2 | Usage or input error (missing/unreadable diff or coverage, malformed diff/coverage JSON, bad flags, input over the 200 MiB limit). |
| 3 | Tool failure (cannot write `--out`, `--self-test` mismatch, `doctor` check failed). |

On failure the tool prints exactly one JSON object to stdout and exits
non-zero:

```json
{"ok": false, "error": {"code": "NO_COVERAGE", "blocked": "...", "fix": "...", "if_that_fails": "..."}}
```

The point of the `fix` / `if_that_fails` fields is that a failure
should tell the caller what to do next, not just what went wrong.
Error codes are `NO_DIFF`, `BAD_DIFF`, `NO_COVERAGE`, `BAD_COVERAGE`,
`BAD_REQUEST`, `INPUT_TOO_LARGE`, `IO_ERROR`, `NO_FIXTURES`,
`TOOL_ERROR`. Schemas live in `schema/`.

## Telemetry

One JSON line to stderr per run. It carries sizes and the outcome
only, never file contents:

```json
{"tool": "testpick", "version": "0.1.0", "command": "select",
 "duration_ms": 123, "input_bytes": 4567, "output_bytes": 890, "ok": true}
```

## Limits (from `plugin.toml`)

- `limits.input_mb = 200` — the diff plus coverage inputs together
  must fit in 200 MiB; larger inputs fail with `INPUT_TOO_LARGE`
  (exit 2). Verified: a 205 MiB diff fails in 0.55 s.
- `limits.timeout_s = 30` — enforced by the harness, not by the tool
  (the tool has no internal timeout). Measured: selecting from 5,000
  tests across 200 files takes ~0.22 s end to end, far under the limit.
- `permissions.network = false` — the tool never touches the network.

## What testpick is not

- **Not a test runner.** It never executes a test.
- **Not a coverage collector.** It never runs `pytest`, `vitest`, or
  `go test`. If a language already has a selector
  (`pytest-testmon`, `vitest --changed`), the right move is a coverage
  adapter that converts its output to the small schema, not a
  reimplementation of coverage collection.
- **Not an MCP server.** The harness uses the CLI through an adapter.
- **Not a model client.** No model calls, no LLM SDK, no API keys, no
  prompt files.
- **Not a static analyser.** It does not build an import graph or
  guess dependencies. If the coverage map is stale or incomplete, the
  fail-safe (run everything) is what protects the caller — see
  [DECISIONS.md](DECISIONS.md).
- **Not a daemon.** One static binary, one run, one JSON object, exit.

## Correctness bar

A selection technique is *safe* when the selected subset includes
every test whose behaviour may be affected by the change (the
classical regression-test-selection definition; see
[RESEARCH.md](RESEARCH.md)). testpick is safe in exactly one sense:
when it can see the change in the coverage map, it selects by
line-level coverage; when it cannot see part of the change
(`unmapped`), the default is to select everything. The unsafe case
it cannot detect is a **stale map** — a test whose coverage changed
since the map was recorded. That is why the harness should regenerate
the map regularly and run the full suite on a schedule. testpick
itself cannot fix stale input, and it says so in the README.
