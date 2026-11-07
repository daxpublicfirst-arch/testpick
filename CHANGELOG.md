# Changelog

All notable changes to testpick are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project
uses [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.1.0] - 2026-11-08

### Added

- `select`: pick the tests that reach a unified diff from a per-test
  coverage map, with `--fail-safe all` (default) / `none`.
- `record`: append a missed test to `<out>/missed.jsonl`.
- `self-test`: run every fixture in `fixtures/` and compare against the
  expected JSON, including both fail-safe modes and `record`.
- `doctor`: check the runtime (OS, temp directory, executable, cwd).
- `version`: print `testpick 0.1.0` (or a JSON object with `--json`).
- `--request <envelope.json>`: optional `arabica-plugin-v1` adapter
  envelope as an alternative to flags.
- One JSON telemetry line to stderr per run; one JSON object to stdout.
- Exit codes: 0 success, 1 no test covers the change, 2 usage/input
  error, 3 tool failure.
- `plugin.toml` (`arabica-plugin-v1`), JSON schemas in `schema/`,
  fixtures in `fixtures/`, coverage adapters in `adapters/`.
- Static linux-amd64 and linux-arm64 binaries, tarballs, SHA256SUMS.
- CI (GitHub Actions) running `go test`, `--self-test`, and `doctor`.
