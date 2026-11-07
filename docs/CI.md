# testpick — CI

## The workflow

The CI workflow is `.github/workflows/ci.yml` in a checkout. It runs
on every push to `main` and on pull requests:

```yaml
name: CI
on:
  push:
    branches: [main]
  pull_request:
    branches: [main]
jobs:
  build-and-test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: "1.23"
      - run: gofmt -l .
      - run: go vet ./...
      - run: go test ./...
      - run: make build
      - run: ./bin/testpick self-test
      - run: ./bin/testpick doctor
```

A verbatim copy is kept at [`ci.yml`](ci.yml) in this directory so
the workflow survives even though the file below cannot be pushed by
automation (see below).

## Why the workflow file is not on `main` yet

The Muse GitHub App — the integration that pushes this repository's
commits — does not have the `workflows` permission on
`daxpublicfirst-arch/testpick`. Pushing any file under
`.github/workflows/` returns `403`. This was verified for this repo
and for the sibling tools (`logreduce`, `agentstats`, `agentbench`,
`lease`): the App can push code, docs, and fixtures, but not workflow
files. It also cannot create tags or GitHub Releases (no such tool in
its catalogue).

## How to install CI (one copy, from a checkout you own)

From a local checkout, with an account that has workflow permission
on the repo:

```sh
mkdir -p .github/workflows
cp docs/ci.yml .github/workflows/ci.yml
git add .github/workflows/ci.yml
git commit -m "Add CI workflow"
git push origin main
```

Or create the file through the GitHub web UI at
`https://github.com/daxpublicfirst-arch/testpick/new/main` with path
`.github/workflows/ci.yml` and the contents of `docs/ci.yml`.

## What CI does not cover

The workflow runs the unit tests, the build, `--self-test`, and
`doctor` on linux-amd64. It does not run the ARM64 QEMU checks, the
perf script, or the mock harness. Those are documented in
[TESTING.md](TESTING.md) and can be added as jobs later:

- ARM64: install `qemu-user-static`, run `make test-arm64`.
- Perf: run `python3 scripts/gen_perf_input.py --binary ./bin/testpick`.
- Mock harness: run `python3 examples/mock-harness.py`.

## Local equivalent

Everything CI runs can be run locally with the same commands (see
[TESTING.md](TESTING.md)). The pre-push discipline used for this repo
is: fresh clone, fresh toolchain, `gofmt`, `go vet`, `go test ./...`,
`make build`, `--self-test`, `doctor`, mock harness, `make release`.
