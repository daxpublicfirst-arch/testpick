// Command testpick selects tests from a diff and a per-test coverage map,
// and records missed tests. It is a standalone CLI: it reads local files
// only, makes no network or model calls, and knows nothing about any
// harness that may wrap it.
//
// Usage:
//
//	testpick select --diff <patch> --coverage <dir> --out <dir> [--fail-safe all|none] [--json]
//	testpick record --missed <test> --out <dir> [--json]
//	testpick self-test [--json]
//	testpick doctor [--json]
//	testpick version [--json]
//	testpick [--request <envelope.json>]
//
// Exit codes: 0 success, 1 no test covers the change (not an error; the
// caller decides), 2 usage or input error, 3 tool failure.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/daxpublicfirst-arch/testpick/internal/pick"
)

// maxInputBytes is the plugin.toml limits.input_mb (200 MiB) applied to the
// diff plus coverage inputs combined.
const maxInputBytes = 200 << 20

// osExecutable locates the running binary. It is a variable (not a direct
// os.Executable call) so tests can point self-test and doctor at a built
// binary instead of the test binary.
var osExecutable = os.Executable

// resolvedCommand is the command run() settled on, including one read
// from a --request envelope. main() uses it for the telemetry line.
var resolvedCommand string

func main() {
	start := time.Now()
	code, inputBytes, outputBytes, ok := run(os.Args[1:])
	command := resolvedCommand
	if command == "" {
		command = commandOf(os.Args[1:])
	}
	telemetry := map[string]any{
		"tool":         pick.ToolName,
		"version":      pick.Version,
		"command":      command,
		"duration_ms":  time.Since(start).Milliseconds(),
		"input_bytes":  inputBytes,
		"output_bytes": outputBytes,
		"ok":           ok,
	}
	// One JSON line to stderr per run. Telemetry never carries file
	// contents, only sizes and the outcome.
	enc := json.NewEncoder(os.Stderr)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(telemetry)
	os.Exit(code)
}

func commandOf(args []string) string {
	boolFlags := map[string]bool{"json": true, "help": true, "version": true}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "-") {
			return a
		}
		if a == "--" {
			if i+1 < len(args) {
				return args[i+1]
			}
			return ""
		}
		name := strings.TrimPrefix(a, "--")
		if j := strings.Index(name, "="); j >= 0 {
			continue
		}
		if boolFlags[name] {
			continue
		}
		i++ // skip this flag's value
	}
	return ""
}

// run executes one command. It returns the exit code, input bytes,
// output bytes, and whether the run succeeded.
func run(args []string) (code int, inputBytes int64, outputBytes int, ok bool) {
	flags, positional, err := parseFlags(args)
	if err != nil {
		return fail(err, 2)
	}
	// --request may supply the command and its arguments from an adapter
	// envelope. Flags on the command line win over envelope values.
	if reqPath := flags["request"]; reqPath != "" {
		env, err := loadRequest(reqPath)
		if err != nil {
			return fail(err, 2)
		}
		if len(positional) == 0 {
			positional = []string{env.Command}
		}
		for k, v := range env.Args {
			if _, present := flags[k]; !present {
				flags[k] = v
			}
		}
		if v, ok := flags["fail-safe"]; ok && v != "" {
			_ = v
		}
		if fs, ok := env.Args["fail_safe"]; ok {
			if _, present := flags["fail-safe"]; !present {
				flags["fail-safe"] = fs
			}
		}
	}
	if flags["help"] == "true" || (len(positional) > 0 && positional[0] == "help") {
		n, _ := fmt.Fprint(os.Stdout, usage)
		return 0, 0, n, true
	}
	if flags["version"] == "true" || (len(positional) > 0 && positional[0] == "version") {
		if flags["json"] == "true" {
			n := writeJSON(map[string]any{"ok": true, "tool": pick.ToolName, "version": pick.Version})
			return 0, 0, n, true
		}
		n, _ := fmt.Fprintf(os.Stdout, "%s %s\n", pick.ToolName, pick.Version)
		return 0, 0, n, true
	}
	if len(positional) == 0 {
		return fail(&pick.CodedError{
			Code:    "BAD_REQUEST",
			Blocked: "no command given",
			Fix:     "Run one of: testpick select, record, self-test, doctor, version.",
			IfFails: "Run `testpick help` for usage.",
		}, 2)
	}
	resolvedCommand = positional[0]

	switch positional[0] {
	case "select":
		return runSelect(flags)
	case "record":
		return runRecord(flags)
	case "self-test":
		return runSelfTest(flags)
	case "doctor":
		return runDoctor(flags)
	default:
		return fail(&pick.CodedError{
			Code:    "BAD_REQUEST",
			Blocked: fmt.Sprintf("unknown command %q", positional[0]),
			Fix:     "Run one of: testpick select, record, self-test, doctor, version.",
			IfFails: "Run `testpick help` for usage.",
		}, 2)
	}
}

const usage = `testpick 0.1.0 — select tests from a diff and a per-test coverage map.

Usage:
  testpick select --diff <patch> --coverage <dir> --out <dir> [--fail-safe all|none] [--json]
  testpick record --missed <test> --out <dir> [--json]
  testpick self-test [--json]
  testpick doctor [--json]
  testpick version [--json]
  testpick --request <envelope.json>

Exit codes: 0 success, 1 no test covers the change, 2 usage or input error, 3 tool failure.
`

// parseFlags splits args into --flag value / --flag=value pairs and the
// remaining positional words. Boolean flags (--json, --help, --version)
// take no value.
func parseFlags(args []string) (map[string]string, []string, error) {
	flags := map[string]string{}
	var positional []string
	boolFlags := map[string]bool{"json": true, "help": true, "version": true}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			positional = append(positional, args[i+1:]...)
			break
		}
		if !strings.HasPrefix(a, "--") {
			positional = append(positional, a)
			continue
		}
		name := strings.TrimPrefix(a, "--")
		var value string
		if j := strings.Index(name, "="); j >= 0 {
			value, name = name[j+1:], name[:j]
		} else if boolFlags[name] {
			value = "true"
		} else {
			if i+1 >= len(args) {
				return nil, nil, &pick.CodedError{
					Code:    "BAD_REQUEST",
					Blocked: fmt.Sprintf("flag --%s needs a value", name),
					Fix:     fmt.Sprintf("Pass --%s <value>, for example --%s ./out.", name, name),
					IfFails: "Run `testpick help` for usage.",
				}
			}
			i++
			value = args[i]
		}
		flags[name] = value
	}
	return flags, positional, nil
}

// requestEnvelope is the optional adapter envelope a harness may pass with
// --request instead of flags.
type requestEnvelope struct {
	Protocol string            `json:"protocol"`
	Command  string            `json:"command"`
	Args     map[string]string `json:"args"`
}

func loadRequest(path string) (*requestEnvelope, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, &pick.CodedError{
			Code:    "BAD_REQUEST",
			Blocked: fmt.Sprintf("cannot read request envelope %q: %v", path, err),
			Fix:     "Pass --request <path> pointing at a JSON envelope like {\"protocol\":\"arabica-plugin-v1\",\"command\":\"select\",\"args\":{\"diff\":\"/abs/change.patch\",\"coverage\":\"/abs/coverage\",\"out\":\"/abs/out\"}}.",
			IfFails: "Check the envelope path and re-run.",
		}
	}
	var env requestEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		return nil, &pick.CodedError{
			Code:    "BAD_REQUEST",
			Blocked: fmt.Sprintf("request envelope %q is not valid JSON: %v", path, err),
			Fix:     "Fix the envelope JSON and re-run.",
			IfFails: "Use flags instead of --request.",
		}
	}
	if env.Protocol != "" && env.Protocol != "arabica-plugin-v1" {
		return nil, &pick.CodedError{
			Code:    "BAD_REQUEST",
			Blocked: fmt.Sprintf("request envelope protocol %q is not \"arabica-plugin-v1\"", env.Protocol),
			Fix:     "Set \"protocol\" to \"arabica-plugin-v1\" in the envelope.",
			IfFails: "Use flags instead of --request.",
		}
	}
	if env.Command == "" {
		return nil, &pick.CodedError{
			Code:    "BAD_REQUEST",
			Blocked: "request envelope has no \"command\"",
			Fix:     "Set \"command\" to one of select, record, self-test, doctor, version.",
			IfFails: "Use flags instead of --request.",
		}
	}
	if env.Args == nil {
		env.Args = map[string]string{}
	}
	return &env, nil
}

// writeJSON writes one JSON object to stdout and returns its byte length.
func writeJSON(v any) int {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		// Encoding a map/struct built in this process cannot fail in
		// practice; fall back to a minimal failure object.
		fmt.Fprint(os.Stdout, `{"ok":false,"error":{"code":"TOOL_ERROR","blocked":"failed to encode output","fix":"Re-run the command.","if_that_fails":"Report the command and its inputs."}}`+"\n")
		return 0
	}
	n, _ := os.Stdout.Write(buf.Bytes())
	return n
}

// fail prints one JSON error object to stdout and returns the exit code.
func fail(err error, code int) (int, int64, int, bool) {
	n := writeJSON(pick.ErrorObject(err))
	return code, 0, n, false
}

func runSelect(flags map[string]string) (int, int64, int, bool) {
	diffPath, covDir, outDir := flags["diff"], flags["coverage"], flags["out"]
	failSafe := flags["fail-safe"]
	if failSafe == "" {
		failSafe = pick.FailSafeAll
	}
	if failSafe != pick.FailSafeAll && failSafe != pick.FailSafeNone {
		return fail(&pick.CodedError{
			Code:    "BAD_REQUEST",
			Blocked: fmt.Sprintf("--fail-safe must be \"all\" or \"none\", got %q", failSafe),
			Fix:     "Pass --fail-safe all (default) or --fail-safe none.",
			IfFails: "Omit --fail-safe to use the default.",
		}, 2)
	}
	for name, value := range map[string]string{"--diff": diffPath, "--coverage": covDir, "--out": outDir} {
		if value == "" {
			return fail(&pick.CodedError{
				Code:    "BAD_REQUEST",
				Blocked: fmt.Sprintf("select requires %s <value>", name),
				Fix:     "Run: testpick select --diff <patch> --coverage <dir> --out <dir> [--fail-safe all|none].",
				IfFails: "Run `testpick help` for usage.",
			}, 2)
		}
	}

	diffData, err := os.ReadFile(diffPath)
	if err != nil {
		return fail(&pick.CodedError{
			Code:    "NO_DIFF",
			Blocked: fmt.Sprintf("cannot read diff file %q: %v", diffPath, err),
			Fix:     "Pass --diff <patch> pointing at a unified diff file, as produced by git diff or diff -u.",
			IfFails: "Check the diff path exists and is readable, then re-run.",
		}, 2)
	}
	cov, covBytes, err := pick.LoadCoverage(covDir)
	if err != nil {
		return fail(err, 2)
	}
	inputBytes := int64(len(diffData)) + covBytes
	if inputBytes > maxInputBytes {
		return fail(&pick.CodedError{
			Code:    "INPUT_TOO_LARGE",
			Blocked: fmt.Sprintf("inputs total %d bytes, over the 200 MiB limit", inputBytes),
			Fix:     "Narrow the diff or split the coverage directory, then re-run.",
			IfFails: "Select tests for a smaller change at a time.",
		}, 2)
	}
	ch, err := pick.ParseDiff(diffData)
	if err != nil {
		return fail(&pick.CodedError{
			Code:    "BAD_DIFF",
			Blocked: fmt.Sprintf("cannot parse diff file %q: %v", diffPath, err),
			Fix:     "Pass a unified diff with +++ b/<path> lines and @@ -a,b +c,d @@ hunk headers.",
			IfFails: "Regenerate the diff with git diff and re-run.",
		}, 2)
	}
	sel := pick.Select(ch, cov, failSafe)

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fail(&pick.CodedError{
			Code:    "IO_ERROR",
			Blocked: fmt.Sprintf("cannot create output directory %q: %v", outDir, err),
			Fix:     "Pass an --out directory that can be created and written to.",
			IfFails: "Check permissions on the parent directory and re-run.",
		}, 3)
	}
	// selection.json holds the same object printed to stdout.
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(sel); err != nil {
		return fail(err, 3)
	}
	if err := os.WriteFile(filepath.Join(outDir, "selection.json"), buf.Bytes(), 0o644); err != nil {
		return fail(&pick.CodedError{
			Code:    "IO_ERROR",
			Blocked: fmt.Sprintf("cannot write selection.json in %q: %v", outDir, err),
			Fix:     "Pass an --out directory that can be written to.",
			IfFails: "Check disk space and permissions, then re-run.",
		}, 3)
	}
	txt := strings.Join(sel.Selected, "\n")
	if txt != "" {
		txt += "\n"
	}
	if err := os.WriteFile(filepath.Join(outDir, "selection.txt"), []byte(txt), 0o644); err != nil {
		return fail(&pick.CodedError{
			Code:    "IO_ERROR",
			Blocked: fmt.Sprintf("cannot write selection.txt in %q: %v", outDir, err),
			Fix:     "Pass an --out directory that can be written to.",
			IfFails: "Check disk space and permissions, then re-run.",
		}, 3)
	}
	n, _ := os.Stdout.Write(buf.Bytes())
	return sel.ExitCode(), inputBytes, n, true
}

func runRecord(flags map[string]string) (int, int64, int, bool) {
	missed, outDir := flags["missed"], flags["out"]
	if outDir == "" {
		return fail(&pick.CodedError{
			Code:    "BAD_REQUEST",
			Blocked: "record requires --out <dir>",
			Fix:     "Run: testpick record --missed <test> --out <dir>.",
			IfFails: "Run `testpick help` for usage.",
		}, 2)
	}
	row, path, err := pick.Record(outDir, missed, time.Now())
	if err != nil {
		// A missing --missed value is a usage error; an I/O failure is a
		// tool failure.
		code := 3
		if ce, ok := err.(*pick.CodedError); ok && ce.Code == "BAD_REQUEST" {
			code = 2
		}
		return fail(err, code)
	}
	n := writeJSON(map[string]any{
		"ok": true, "tool": pick.ToolName, "version": pick.Version,
		"test": row.Test, "at": row.At, "file": path,
	})
	return 0, int64(len(missed)), n, true
}

// doctorCheck is one runtime check reported by doctor.
type doctorCheck struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}

func runDoctor(flags map[string]string) (int, int64, int, bool) {
	checks := []doctorCheck{}
	exe, err := osExecutable()
	if err != nil {
		checks = append(checks, doctorCheck{"executable", false, fmt.Sprintf("cannot locate own binary: %v", err)})
	} else {
		checks = append(checks, doctorCheck{"executable", true, exe})
	}
	if runtime.GOOS == "linux" {
		checks = append(checks, doctorCheck{"os", true, fmt.Sprintf("linux/%s", runtime.GOARCH)})
	} else {
		checks = append(checks, doctorCheck{"os", false, fmt.Sprintf("unsupported OS %q; testpick targets linux", runtime.GOOS)})
	}
	tmp, err := os.MkdirTemp("", "testpick-doctor-*")
	if err != nil {
		checks = append(checks, doctorCheck{"temp_writable", false, fmt.Sprintf("cannot create a temp directory: %v", err)})
	} else {
		probe := filepath.Join(tmp, "probe")
		if werr := os.WriteFile(probe, []byte("ok"), 0o600); werr != nil {
			checks = append(checks, doctorCheck{"temp_writable", false, fmt.Sprintf("cannot write to a temp directory: %v", werr)})
		} else {
			checks = append(checks, doctorCheck{"temp_writable", true, tmp})
		}
		_ = os.RemoveAll(tmp)
	}
	if wd, err := os.Getwd(); err != nil {
		checks = append(checks, doctorCheck{"cwd", false, fmt.Sprintf("cannot read working directory: %v", err)})
	} else {
		checks = append(checks, doctorCheck{"cwd", true, wd})
	}
	allOK := true
	for _, c := range checks {
		if !c.OK {
			allOK = false
		}
	}
	out := map[string]any{
		"ok": true, "tool": pick.ToolName, "version": pick.Version,
		"checks": checks,
	}
	if !allOK {
		out["ok"] = false
		n := writeJSON(out)
		return 3, 0, n, false
	}
	n := writeJSON(out)
	return 0, 0, n, true
}

// selfTestRunner returns the command prefix for the subprocesses
// --self-test spawns. Normally the binary re-executes itself, but under
// cross-architecture emulation (for example qemu-aarch64-static on an
// x86_64 host without binfmt_misc) the host kernel cannot exec the
// binary directly. Setting TESTPICK_SELFTEST_RUNNER to a command (for
// example "qemu-aarch64-static") runs each case as
// "<runner> <binary> <args...>".
func selfTestRunner() []string {
	runner := strings.TrimSpace(os.Getenv("TESTPICK_SELFTEST_RUNNER"))
	if runner == "" {
		return nil
	}
	return strings.Fields(runner)
}

// selfTestCommand builds the exec.Cmd for one self-test case.
func selfTestCommand(exe string, args ...string) *exec.Cmd {
	if prefix := selfTestRunner(); len(prefix) > 0 {
		return exec.Command(prefix[0], append(append([]string{}, prefix[1:]...), append([]string{exe}, args...)...)...)
	}
	return exec.Command(exe, args...)
}

type selfTestCase struct {
	Name string `json:"name"`
	OK   bool   `json:"ok"`
	Note string `json:"note,omitempty"`
}

func runSelfTest(flags map[string]string) (int, int64, int, bool) {
	fixtures, err := findFixtures()
	if err != nil {
		return fail(err, 3)
	}
	exe, err := osExecutable()
	if err != nil {
		return fail(err, 3)
	}
	cases := []selfTestCase{}
	run := func(name string, ok bool, note string) {
		cases = append(cases, selfTestCase{name, ok, note})
	}
	// select cases: fixture, fail-safe, expected file, expected exit code.
	selectCases := []struct {
		name, fixture, failSafe, expected string
		exitCode                          int
	}{
		{"basic/all", "basic", "all", "expected.json", 0},
		{"basic/none", "basic", "none", "expected-none.json", 0},
		{"unmapped/all", "unmapped", "all", "expected-all.json", 0},
		{"unmapped/none", "unmapped", "none", "expected-none.json", 1},
	}
	for _, sc := range selectCases {
		tmp, err := os.MkdirTemp("", "testpick-selftest-*")
		if err != nil {
			run(sc.name, false, fmt.Sprintf("cannot create temp dir: %v", err))
			continue
		}
		defer os.RemoveAll(tmp)
		outDir := filepath.Join(tmp, "out")
		cmd := selfTestCommand(exe, "select",
			"--diff", filepath.Join(fixtures, sc.fixture, "change.patch"),
			"--coverage", filepath.Join(fixtures, sc.fixture, "coverage"),
			"--out", outDir, "--fail-safe", sc.failSafe, "--json")
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		runErr := cmd.Run()
		gotCode := 0
		if runErr != nil {
			if ee, ok := runErr.(*exec.ExitError); ok {
				gotCode = ee.ExitCode()
			} else {
				run(sc.name, false, fmt.Sprintf("cannot run select: %v", runErr))
				continue
			}
		}
		if gotCode != sc.exitCode {
			run(sc.name, false, fmt.Sprintf("exit code %d, want %d (stderr: %s)", gotCode, sc.exitCode, strings.TrimSpace(stderr.String())))
			continue
		}
		expData, err := os.ReadFile(filepath.Join(fixtures, sc.fixture, sc.expected))
		if err != nil {
			run(sc.name, false, fmt.Sprintf("cannot read %s: %v", sc.expected, err))
			continue
		}
		var want, got map[string]any
		if err := json.Unmarshal(expData, &want); err != nil {
			run(sc.name, false, fmt.Sprintf("expected file is not valid JSON: %v", err))
			continue
		}
		if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
			run(sc.name, false, fmt.Sprintf("select output is not valid JSON: %v", err))
			continue
		}
		if note, same := compareSelection(want, got); !same {
			run(sc.name, false, note)
			continue
		}
		// --out files must exist and agree with stdout.
		selJSON, err := os.ReadFile(filepath.Join(outDir, "selection.json"))
		if err != nil || !bytes.Equal(bytes.TrimSpace(selJSON), bytes.TrimSpace(stdout.Bytes())) {
			run(sc.name, false, "selection.json missing or does not match stdout")
			continue
		}
		if _, err := os.ReadFile(filepath.Join(outDir, "selection.txt")); err != nil {
			run(sc.name, false, "selection.txt missing")
			continue
		}
		run(sc.name, true, "")
	}
	// record case: appends a valid JSONL row.
	{
		tmp, err := os.MkdirTemp("", "testpick-selftest-record-*")
		if err != nil {
			run("record", false, fmt.Sprintf("cannot create temp dir: %v", err))
		} else {
			defer os.RemoveAll(tmp)
			outDir := filepath.Join(tmp, "out")
			cmd := selfTestCommand(exe, "record", "--missed", "tests/test_a.py::test_y", "--out", outDir, "--json")
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			if err := cmd.Run(); err != nil {
				run("record", false, fmt.Sprintf("record failed: %v (stderr: %s)", err, strings.TrimSpace(stderr.String())))
			} else {
				rows, err := os.ReadFile(filepath.Join(outDir, "missed.jsonl"))
				if err != nil {
					run("record", false, "missed.jsonl missing")
				} else {
					var row map[string]any
					lines := strings.Split(strings.TrimSpace(string(rows)), "\n")
					if len(lines) != 1 || json.Unmarshal([]byte(lines[0]), &row) != nil {
						run("record", false, "missed.jsonl does not hold exactly one valid JSON row")
					} else if row["test"] != "tests/test_a.py::test_y" || row["reason"] != "missed" {
						run("record", false, fmt.Sprintf("missed.jsonl row has wrong fields: %s", strings.TrimSpace(string(rows))))
					} else if _, err := time.Parse(time.RFC3339, fmt.Sprint(row["at"])); err != nil {
						run("record", false, fmt.Sprintf("missed.jsonl row has a bad timestamp: %v", row["at"]))
					} else {
						run("record", true, "")
					}
				}
			}
		}
	}

	allOK := true
	for _, c := range cases {
		if !c.OK {
			allOK = false
		}
	}
	out := map[string]any{
		"ok": true, "tool": pick.ToolName, "version": pick.Version,
		"cases": cases,
	}
	if !allOK {
		out["ok"] = false
		n := writeJSON(out)
		return 3, 0, n, false
	}
	n := writeJSON(out)
	return 0, 0, n, true
}

// compareSelection compares the fields that define a selection. It
// ignores extra fields in the actual output.
func compareSelection(want, got map[string]any) (string, bool) {
	for _, key := range []string{"ok", "tool", "version", "reason", "fail_safe"} {
		if fmt.Sprint(want[key]) != fmt.Sprint(got[key]) {
			return fmt.Sprintf("field %q: got %v, want %v", key, got[key], want[key]), false
		}
	}
	for _, key := range []string{"selected", "unmapped"} {
		if !stringSlicesEqual(toStrings(want[key]), toStrings(got[key])) {
			return fmt.Sprintf("field %q: got %v, want %v", key, got[key], want[key]), false
		}
	}
	return "", true
}

func toStrings(v any) []string {
	arr, _ := v.([]any)
	out := make([]string, 0, len(arr))
	for _, item := range arr {
		out = append(out, fmt.Sprint(item))
	}
	sort.Strings(out)
	return out
}

func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// findFixtures locates the fixtures/ directory: an explicit
// $TESTPICK_FIXTURES path first, then locations relative to the running
// binary, then the working directory.
func findFixtures() (string, error) {
	candidates := []string{}
	if env := os.Getenv("TESTPICK_FIXTURES"); env != "" {
		candidates = append(candidates, env)
	}
	if exe, err := osExecutable(); err == nil {
		dir := filepath.Dir(exe)
		candidates = append(candidates,
			filepath.Join(dir, "fixtures"),
			filepath.Join(dir, "..", "fixtures"),
		)
	}
	candidates = append(candidates, "fixtures", filepath.Join("..", "fixtures"))
	for _, c := range candidates {
		if st, err := os.Stat(filepath.Join(c, "basic", "change.patch")); err == nil && !st.IsDir() {
			abs, err := filepath.Abs(c)
			if err == nil {
				return abs, nil
			}
			return c, nil
		}
	}
	return "", &pick.CodedError{
		Code:    "NO_FIXTURES",
		Blocked: "cannot find the fixtures/ directory next to the binary or in the working directory",
		Fix:     "Run --self-test from a testpick checkout, or set TESTPICK_FIXTURES to the fixtures directory.",
		IfFails: "Reinstall testpick from a checkout that includes fixtures/.",
	}
}
