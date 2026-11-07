// Package pick implements the core of testpick: parsing unified diffs,
// loading per-test coverage maps, and selecting the tests that reach a
// change. It has no network access, makes no model calls, and reads only
// local files. It knows nothing about any harness that may wrap the CLI.
package pick

// Version is the tool version. It is printed by --version and embedded in
// every JSON output and telemetry line.
const Version = "0.1.0"

// ToolName is the tool name used in JSON output and telemetry.
const ToolName = "testpick"
