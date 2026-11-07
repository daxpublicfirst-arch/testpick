# testpick — Decisions

A log of the decisions that shape this tool: the context, what was
chosen, what was rejected, and the trade-off. Read this before
changing a behaviour — the "why" is here so a future reader does not
have to guess or re-litigate it.

## D1. Go, stdlib only, one static binary

- **Context:** The plugin contract requires one static binary, no
  daemon, no network, no model calls. The tool must run on
  linux-amd64 and linux-arm64 from a tarball with nothing to install.
- **Decision:** Go 1.23, standard library only, `CGO_ENABLED=0`.
- **Alternatives considered:** Python (simpler to write, but needs a
  runtime on the target and is slower to start for a CLI that must
  finish inside 30 s); Rust (same properties as Go, but the team
  writes Go for the sibling tools and Go's cross-compilation is one
  flag).
- **Trade-off:** Go binaries are larger (~2.5 MB) than a Python
  script, and Go's error handling is verbose. In exchange there is no
  runtime dependency at all on the target machine.
- **Status:** Implemented. `go.mod` has no dependencies.

## D2. The small coverage schema is the only input shape

- **Context:** Every language already has coverage tools
  (`coverage.py`, `vitest --coverage`, `go test -coverprofile`), and
  every one of them emits a different format.
- **Decision:** The core reads exactly one shape —
  `{"test", "files", "lines"}` — one JSON file per test. Language
  adapters convert existing output into this shape
  (`adapters/coverage_to_testpick.py`).
- **Alternatives considered:** (a) Reading `coverage.py` JSON,
  `coverprofile`, and `lcov` natively in the core. Rejected: it would
  make the core depend on the quirks of every coverage tool, and the
  task prompt explicitly says to write an adapter instead of
  reimplementing coverage collection. (b) A richer schema with
  per-file line maps. Rejected: the small shape is enough for
  selection, and a richer shape is harder for adapters to produce.
- **Trade-off:** Adapter authors must do the conversion, and the
  conversion can lose information (for example, `coverage.py` reports
  whole-suite coverage, so a per-test map needs one run per test).
  That loss is documented in the adapter's docstring, not hidden.

## D3. In a multi-file map, `lines` applies to every listed file

- **Context:** The schema `{"files": [...], "lines": [...]}` does not
  say which lines belong to which file.
- **Decision:** `lines` applies to every file in `files`. A map that
  needs different lines per file must be split into one entry per
  file (entries may share a test name).
- **Alternatives considered:** Requiring one file per entry.
  Rejected: it would forbid the common file-level case and make
  fixtures noisier. Treating `lines` as belonging to the first file
  only. Rejected: silent and surprising.
- **Trade-off:** A map with several files and one line list can
  over-select (a test is selected if any of its files matches a
  changed line). Over-selection is safe — it runs a test that did not
  need to run — while under-selection is not. Documented in README
  and in `coverage.go`.

## D4. Fail-safe default is `all`

- **Context:** When a changed file has no coverage at all
  (`unmapped`), the tool cannot know which tests are affected.
- **Decision:** Default `--fail-safe all`: select every test in the
  coverage directory. `--fail-safe none` keeps only exact matches and
  exits 1 so the caller decides.
- **Alternatives considered:** Default `none`. Rejected: it would
  silently skip the uncovered change, which is exactly the failure
  mode test selection exists to prevent. This mirrors Microsoft's
  Test Impact Analysis, whose documented guardrail is to fall back to
  running all tests when it cannot reason about a change (see
  [RESEARCH.md](RESEARCH.md)).
- **Trade-off:** The default can be slow (it runs everything) on
  changes touching untested files. That is the price of safety, and
  the caller can opt into `none` when it has its own fallback.

## D5. Exit 1 means "no test covers the change", not an error

- **Context:** The contract defines exit 1 separately from errors
  (2) and tool failures (3), but does not spell out every case.
- **Decision:** Exit 1 when nothing was selected, or when fail-safe
  is `none` and any changed file is unmapped — even if some other
  change matched exactly. Exit 0 when fail-safe `all` turns an
  unmapped file into a full selection (a conservative success).
  An empty diff also exits 1 (`reason: "no changed files"`).
- **Alternatives considered:** Exit 0 whenever selection "worked",
  including an empty selection. Rejected: the caller needs to
  distinguish "nothing to run" from "here are your tests" without
  parsing JSON, and the acceptance criteria require exit 1 for
  unmapped + `none`.
- **Trade-off:** Callers must handle exit 1 as a normal outcome, not
  as a failure. Documented in README and in the `ExitCode()` comment.

## D6. Failures are one JSON object with a fix

- **Context:** The harness reads stdout as JSON; a stack trace or a
  bare message would break its parsing.
- **Decision:** Every failure prints
  `{"ok": false, "error": {"code", "blocked", "fix", "if_that_fails"}}`
  and exits 2 (input/usage) or 3 (tool failure).
- **Alternatives considered:** Plain-text errors on stderr.
  Rejected: the contract requires one JSON object on stdout.
- **Trade-off:** Error messages must stay short and must never
  contain file contents (see D11).

## D7. Telemetry on stderr, sizes only

- **Context:** The harness wants per-run timing and sizes without
  parsing the selection output.
- **Decision:** One JSON line to stderr per run with `tool`,
  `version`, `command`, `duration_ms`, `input_bytes`,
  `output_bytes`, `ok`. Never file contents, never test names.
- **Alternatives considered:** Telemetry on stdout alongside the
  result. Rejected: stdout must be exactly one JSON object.
  No telemetry at all. Rejected: the contract requires it.
- **Trade-off:** A run that fails before `run()` resolves a command
  (for example a `--request` envelope that cannot be read) reports
  `command: ""`. Accepted: the failure object itself carries the
  detail.

## D8. No MCP server, no coverage collection, no model

- **Context:** The task prompt forbids all three, and the plugin
  contract is CLI-through-adapter.
- **Decision:** The tool is a CLI only. It never runs a test runner,
  never collects coverage, never calls a model.
- **Trade-off:** The harness must supply the coverage map. That is
  the harness's job (it owns the test runs); testpick only selects.
  If a language already has a selector, write an adapter (D2).

## D9. `--self-test` shells out to the real binary

- **Context:** `--self-test` must prove the shipped binary behaves,
  not just the library.
- **Decision:** Self-test runs the executable (found via
  `os.Executable`, or `TESTPICK_SELFTEST_RUNNER` + executable under
  emulation) as a subprocess for every fixture case and compares
  stdout with the expected JSON, then checks `--out` files agree.
- **Alternatives considered:** Testing the internal functions
  directly. Rejected: that would test the library while the binary
  could still be broken (flag parsing, output files, exit codes).
- **Trade-off:** Self-test is slower (~0.3 s) and needs the fixtures
  next to the binary or at `$TESTPICK_FIXTURES`. It also needs
  `TESTPICK_SELFTEST_RUNNER` under QEMU (see D13).

## D10. Fixtures live in the repo and carry expected outputs

- **Context:** `--self-test` needs ground truth that travels with
  the tool.
- **Decision:** `fixtures/basic` and `fixtures/unmapped`, each with
  `change.patch`, `coverage/*.json`, and `expected*.json`. The
  unmapped fixture has two expected files (fail-safe `all` and
  `none`) because the two modes differ in selection and exit code.
- **Trade-off:** Expected files duplicate the reason strings, so a
  wording change needs a fixture update too. That is intentional:
  wording is part of the contract.

## D11. Never log file contents

- **Context:** Diffs and coverage maps can contain source code and
  test names from the user's repository.
- **Decision:** Errors and telemetry name paths and counts, never
  contents. `blocked` messages quote at most a file path and a parse
  error.
- **Trade-off:** Debugging a malformed input from the error alone is
  sometimes harder. Accepted: the error names the file, and the user
  can inspect it locally.

## D12. Reproducible release builds

- **Context:** `SHA256SUMS.txt` is committed to the repo so anyone
  can verify a downloaded tarball. That only works if rebuilding from
  the same code produces identical hashes.
- **Decision:** `make release` builds with `-buildvcs=false`
  (Go otherwise embeds the VCS revision, so a build from a checkout
  differs from a build from a plain directory) and packs tarballs
  deterministically (`--sort=name`, fixed `--mtime`, `--owner=0
  --group=0`, `GZIP=-n`).
- **Alternatives considered:** Not committing `SHA256SUMS.txt`.
  Rejected: the deliverables require it. Generating it at release
  time only. Rejected for the same reason — the committed file must
  match a rebuild.
- **Trade-off:** The binaries carry no VCS stamp (`go version -m`
  shows no `vcs.revision`). The stamp is not needed: the release
  notes and the commit history record which code was released.
- **Status:** Verified: two consecutive `make release` runs and a
  rebuild from a fresh clone produce byte-identical hashes.

## D13. Cross-architecture self-test via a runner env var

- **Context:** The ARM64 binary must be executed to be trusted, but
  an x86_64 host kernel cannot exec it without `binfmt_misc`, and
  this container's mount namespace does not keep a `binfmt_misc`
  registration (each shell gets a fresh namespace; an attempted
  registration also matched too broadly and broke exec of host
  binaries in that namespace).
- **Decision:** `TESTPICK_SELFTEST_RUNNER` prefixes the subprocess
  command (`qemu-aarch64-static <binary> <args...>`). `make test-arm64`
  uses it. Verified: ARM64 `version`, `doctor`, `select`, and
  `self-test` (5/5) all pass under `qemu-aarch64-static` 8.2.2.
- **Alternatives considered:** Registering `binfmt_misc` in CI
  (for example `docker/setup-qemu-action`). That is the right setup
  for GitHub Actions, where the registration persists for the job;
  the runner env var is the portable fallback that works anywhere
  QEMU exists.
- **Trade-off:** Self-test under QEMU is slower (~0.9 s) and QEMU
  user-mode emulation is not identical to real ARM hardware (syscall
  translation, signal handling). It validates the binary runs and
  selects correctly; it does not validate ARM-specific performance.

## D14. `record` never changes selection

- **Context:** A missed test is a signal that the selection was
  wrong; it would be tempting to feed it back into the next
  selection automatically.
- **Decision:** `record` only appends to `missed.jsonl`. Selection
  reads only `--diff` and `--coverage`. Any feedback loop belongs in
  the harness, with human review, not silently inside the selector.
- **Trade-off:** The harness must do the work of using the log.
  Accepted: silent feedback inside a selector is how selections
  drift without anyone noticing.

## D15. Limits: 200 MiB in, 30 s wall (harness-side)

- **Context:** `plugin.toml` sets `input_mb = 200` and
  `timeout_s = 30`.
- **Decision:** The tool enforces the size limit itself
  (`INPUT_TOO_LARGE`, exit 2) after reading the inputs but before
  parsing the diff. The timeout is enforced by the harness; the tool
  has no internal deadline.
- **Alternatives considered:** Enforcing a timeout inside the tool
  with `context`. Rejected: selection is a single fast pass with no
  natural cancellation point, and the harness already owns the
  timeout for every plugin.
- **Trade-off:** A pathological input under 200 MiB could still be
  slow (for example a huge number of tiny coverage files — each is a
  separate JSON parse). Measured worst case so far: 5,000 coverage
  files select in ~0.22 s end to end. The harness timeout remains the
  backstop.
- **Status:** Verified: a 205 MiB diff is rejected in 0.55 s with
  `INPUT_TOO_LARGE`.

## D16. Plugin contract fields

- **Context:** `plugin.toml` is the harness's discovery document.
- **Decision:** `protocol = "arabica-plugin-v1"`, `kind = "cli"`,
  `entry = "bin/testpick"`, `commands` lists all five commands,
  `permissions = {read = ["--diff", "--coverage"], write = ["--out"],
  network = false}`, `limits = {input_mb = 200, timeout_s = 30}`,
  `license = "MIT"`, `source` is the real repository URL.
- **Alternatives considered:** Leaving `source` as the
  `https://<your-repo>` placeholder from the task prompt. Rejected:
  a placeholder in a shipped file is a bug, not a TODO.
- **Trade-off:** `entry = "bin/testpick"` assumes the harness
  installs the binary at `bin/testpick` (the release tarballs contain
  the binary at the top level; the harness lays it out). Documented
  here so the assumption is visible.
