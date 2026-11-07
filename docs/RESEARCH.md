# testpick — Research

What was read before and during this build, and what each source
changed about the tool. Sources are real and checkable; nothing here
is invented. Where a source disagrees with a testpick choice, the
disagreement is noted rather than hidden.

## 1. Regression test selection (RTS): the classical framing

- **Ekstazi** — Gligoric et al., "Ekstazi: Lightweight Test
  Selection" (2015). The correctness bar testpick uses: a technique
  is *safe* if "the subset of selected tests includes all tests whose
  behavior may be affected by the changes". The economic motivation is
  also classical: linear growth in changes times linear growth in
  suite runtime gives quadratic total test cost.
  - Paper: <https://users.ece.utexas.edu/~gligoric/papers/GligoricETAL15Ekstazi.pdf>
  - Influence: the `unmapped` + fail-safe-`all` rule. testpick is
    safe in the Ekstazi sense only when the coverage map is complete;
    the fail-safe is the admission that it sometimes is not.
- **Microsoft Test Impact Analysis (TIA)** — "Speed up testing by
  using Test Impact Analysis", Microsoft Learn. TIA selects from a
  dependency map of the form test → {files it exercised}, and ships
  three guardrails: select impacted tests, previously failing tests,
  and newly added tests; **for commits it cannot understand, fall
  back to running all tests**; and run the full suite on a configured
  schedule.
  - Docs: <https://learn.microsoft.com/en-us/azure/devops/pipelines/test/test-impact-analysis>
  - Influence: the dependency-map shape is exactly testpick's small
    schema, and the safe fallback is testpick's default fail-safe.
    testpick does **not** implement the other two guardrails
    (previously failing, newly added tests) — that state belongs to
    the harness, which owns test history. See DECISIONS.md D4/D14.
- **Google, "Taming Google-Scale Continuous Testing"** — Memon et
  al., ICSE-SEIP 2017. Google's Test Automation Platform ran about
  150 million test executions a day; only 1.23% of test executions
  ever caught a breakage or a fix.
  - Paper: <https://research.google/pubs/taming-google-scale-continuous-testing/>
  - Influence: the scale argument for selection at all. It also sets
    expectations: even a good selector's output is mostly green; the
    value is time saved, not failures found.
- **Meta, "Predictive test selection"** — build-dependency-based
  selection as the conventional baseline, with a learned model on
  top to predict which tests are likely to fail.
  - Influence: deliberately **not** followed. testpick has no model,
    no training data, and no build graph. Predictive selection trades
    safety for speed; testpick trades speed for safety (fail-safe
    `all`). A harness that wants prediction can layer it on top of
    testpick's exact-match selection.

## 2. Existing selectors: adapt, don't reimplement

- **pytest-testmon** — a pytest plugin that records which blocks of
  code each test executes (via coverage.py) and re-runs only the
  tests whose blocks changed, plus tests that failed last time.
  - Docs: <https://www.testmon.org/>, PyPI: <https://pypi.org/project/pytest-testmon/>
  - What it taught us: line/block-level coverage is the right
    granularity (file-level alone over-selects); a selector needs a
    full run first before it can select anything; and it **does not
    track non-code files** (templates, JSON, text) or services reached
    over the network — a change to those files makes testmon say
    nothing needs to run. testpick has the same blind spot by design:
    if a changed file is not in the coverage map it is `unmapped`,
    and the fail-safe runs everything instead of nothing. That
    asymmetry (fail-safe runs all, testmon runs none for untracked
    files) is intentional and documented.
  - Why an adapter: testmon already collects the dependency data.
    `adapters/coverage_to_testpick.py` converts `coverage.py` JSON
    into testpick's schema instead of re-collecting it.
- **vitest `--changed` / Jest `--changedSince`** — select tests by
  `git diff`, at file granularity, using the test runner's own
  dependency graph.
  - Influence: file granularity is the fallback, line granularity is
    the precision. testpick's file-level map (no `lines`) is the
    equivalent of this mode.
- **Go** — `go test` has no built-in affected-test selection; the
  usual workaround is `go test $(go list ./... | grep -f <(git diff
  --name-only ...))`, which is file-granularity and shell-glue.
  testpick's `gocover` adapter converts `go test -coverprofile`
  output instead.

## 3. Cross-architecture testing with QEMU

- **qemu-user-static** — user-mode emulation runs a binary compiled
  for another architecture on the host: `qemu-aarch64-static
  ./my-program-arm64`. The standard install is
  `apt install qemu-user-static` (Debian/Ubuntu); CI commonly uses
  it to test cross-compiled outputs. A static binary needs no `-L`
  sysroot prefix; a dynamically linked one does
  (`-L /usr/aarch64-linux-gnu`).
  - Sources: QEMU user-mode docs (<https://www.qemu.org/docs/master/user/main.html>),
    Microsoft Rust engineering book on cross-compilation
    (<https://microsoft.github.io/RustTraining/engineering-book/ch02-cross-compilation-one-source-many-target.html>),
    and the `mini-init-asm` project's ARM64-via-QEMU notes.
  - What was verified here: `qemu-aarch64-static` 8.2.2 runs the
    ARM64 testpick binary — `version`, `doctor` (reports
    `linux/arm64`), `select`, and `self-test` (5/5) all pass. See
    [TESTING.md](TESTING.md).
  - What did **not** work here: `binfmt_misc` registration. This
    container gives each shell a fresh mount namespace, so a mount
    of `/proc/sys/fs/binfmt_misc` does not survive to the next shell;
    an attempted registration also matched too broadly and broke
    exec of host binaries inside that namespace. The portable answer
    is `TESTPICK_SELFTEST_RUNNER` (see DECISIONS.md D13). On GitHub
    Actions the standard `docker/setup-qemu-action` registration
    persists for the job and is the right setup there.

## 4. Go-specific research

- **Testing a `main` package.** Go's `go test` compiles the package
  under test, so a CLI's `main()` cannot be called from tests
  without exiting the test process. The standard approaches are
  (a) move logic into a testable package, or (b) make `main()` thin
  and test the functions it calls. testpick does both: selection
  lives in `internal/pick`, and the CLI's `run()` returns an exit
  code instead of calling `os.Exit`, so `cmd/testpick` tests capture
  stdout with a pipe and assert on exit codes. `os.Executable` is
  behind a variable for the same reason (`--self-test` re-executes
  the binary; under `go test` that binary is the test runner).
- **Reproducible builds.** Go embeds VCS info (`vcs.revision`,
  `vcs.time`) in binaries built from a checkout. `-buildvcs=false`
  removes it; combined with `-trimpath`, builds from different
  directories are identical. Tarball determinism needs sorted
  entries, a fixed `--mtime`, fixed owner/group, and `GZIP=-n`
  (gzip otherwise stores the current time). Verified: two builds and
  a rebuild from a fresh clone give identical `SHA256SUMS.txt`.
