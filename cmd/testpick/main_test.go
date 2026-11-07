package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// captureStdout runs f with os.Stdout redirected to a pipe and returns
// everything f wrote. run() writes its JSON to os.Stdout, so this is how
// the CLI is tested without spawning a process.
func captureStdout(t *testing.T, f func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	f()
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	os.Stdout = old
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func TestParseFlags(t *testing.T) {
	flags, pos, err := parseFlags([]string{"select", "--diff", "a.patch", "--fail-safe=all", "--json"})
	if err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	if len(pos) != 1 || pos[0] != "select" {
		t.Errorf("positional = %v, want [select]", pos)
	}
	if flags["diff"] != "a.patch" || flags["fail-safe"] != "all" || flags["json"] != "true" {
		t.Errorf("flags = %v", flags)
	}
}

func TestParseFlagsMissingValue(t *testing.T) {
	if _, _, err := parseFlags([]string{"select", "--diff"}); err == nil {
		t.Error("expected an error when --diff has no value")
	}
}

func TestParseFlagsSeparator(t *testing.T) {
	flags, pos, err := parseFlags([]string{"--", "--json"})
	if err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	if len(pos) != 1 || pos[0] != "--json" {
		t.Errorf("positional after -- = %v, want [--json]", pos)
	}
	if len(flags) != 0 {
		t.Errorf("flags = %v, want empty", flags)
	}
}

func TestCommandOf(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"select", "--diff", "x"}, "select"},
		{[]string{"--json", "doctor"}, "doctor"},
		{[]string{"--request", "/tmp/req.json"}, ""},
		{[]string{"--request=/tmp/req.json", "version"}, "version"},
		{[]string{}, ""},
	}
	for _, c := range cases {
		if got := commandOf(c.args); got != c.want {
			t.Errorf("commandOf(%v) = %q, want %q", c.args, got, c.want)
		}
	}
}

func TestLoadRequest(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good.json")
	if err := os.WriteFile(good, []byte(`{"protocol":"arabica-plugin-v1","command":"select","args":{"diff":"a.patch","coverage":"cov","out":"out"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	env, err := loadRequest(good)
	if err != nil {
		t.Fatalf("loadRequest: %v", err)
	}
	if env.Command != "select" || env.Args["diff"] != "a.patch" {
		t.Errorf("envelope = %+v", env)
	}

	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte(`{"protocol":"other","command":"select"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadRequest(bad); err == nil {
		t.Error("expected an error for a wrong protocol")
	}
	if _, err := loadRequest(filepath.Join(dir, "missing.json")); err == nil {
		t.Error("expected an error for a missing envelope file")
	}
}

func TestRunVersion(t *testing.T) {
	var code int
	out := captureStdout(t, func() {
		code, _, _, _ = run([]string{"version"})
	})
	if code != 0 || out != "testpick 0.1.0\n" {
		t.Errorf("version: code=%d out=%q", code, out)
	}
	out = captureStdout(t, func() {
		code, _, _, _ = run([]string{"version", "--json"})
	})
	var obj map[string]any
	if err := json.Unmarshal([]byte(out), &obj); err != nil {
		t.Fatalf("version --json is not JSON: %v", err)
	}
	if obj["version"] != "0.1.0" || obj["ok"] != true {
		t.Errorf("version --json = %v", obj)
	}
}

func TestRunHelpAndUnknown(t *testing.T) {
	var code int
	out := captureStdout(t, func() {
		code, _, _, _ = run([]string{"help"})
	})
	if code != 0 || !strings.Contains(out, "testpick select") {
		t.Errorf("help: code=%d out=%q", code, out)
	}
	out = captureStdout(t, func() {
		code, _, _, _ = run([]string{"frobnicate"})
	})
	if code != 2 {
		t.Errorf("unknown command: code=%d, want 2", code)
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(out), &obj); err != nil {
		t.Fatalf("error output is not JSON: %v", err)
	}
	if errObj, ok := obj["error"].(map[string]any); !ok || errObj["code"] != "BAD_REQUEST" {
		t.Errorf("error object = %v, want BAD_REQUEST", obj["error"])
	}
	out = captureStdout(t, func() {
		code, _, _, _ = run([]string{})
	})
	if code != 2 {
		t.Errorf("no command: code=%d, want 2", code)
	}
}

func TestRunSelectEndToEnd(t *testing.T) {
	outDir := filepath.Join(t.TempDir(), "out")
	var code int
	out := captureStdout(t, func() {
		code, _, _, _ = run([]string{"select",
			"--diff", "../../fixtures/basic/change.patch",
			"--coverage", "../../fixtures/basic/coverage",
			"--out", outDir, "--json"})
	})
	if code != 0 {
		t.Fatalf("select: code=%d out=%s", code, out)
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(out), &obj); err != nil {
		t.Fatalf("select output is not JSON: %v", err)
	}
	sel, _ := obj["selected"].([]any)
	if len(sel) != 1 || sel[0] != "tests/test_a.py::test_x" {
		t.Errorf("selected = %v", obj["selected"])
	}
	// selection.json on disk must be the same object as stdout.
	disk, err := os.ReadFile(filepath.Join(outDir, "selection.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(disk)) != strings.TrimSpace(out) {
		t.Errorf("selection.json differs from stdout")
	}
	txt, err := os.ReadFile(filepath.Join(outDir, "selection.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(txt) != "tests/test_a.py::test_x\n" {
		t.Errorf("selection.txt = %q", string(txt))
	}
}

func TestRunSelectUnmappedNoneExits1(t *testing.T) {
	outDir := filepath.Join(t.TempDir(), "out")
	var code int
	out := captureStdout(t, func() {
		code, _, _, _ = run([]string{"select",
			"--diff", "../../fixtures/unmapped/change.patch",
			"--coverage", "../../fixtures/unmapped/coverage",
			"--out", outDir, "--fail-safe", "none", "--json"})
	})
	if code != 1 {
		t.Errorf("unmapped + fail-safe none: code=%d, want 1 (out=%s)", code, out)
	}
}

func TestRunSelectErrors(t *testing.T) {
	cases := []struct {
		name string
		args []string
		code int
		err  string
	}{
		{"missing diff", []string{"select", "--diff", "nope.patch", "--coverage", "../../fixtures/basic/coverage", "--out", t.TempDir()}, 2, "NO_DIFF"},
		{"missing coverage", []string{"select", "--diff", "../../fixtures/basic/change.patch", "--coverage", "nope-dir", "--out", t.TempDir()}, 2, "NO_COVERAGE"},
		{"bad fail-safe", []string{"select", "--diff", "../../fixtures/basic/change.patch", "--coverage", "../../fixtures/basic/coverage", "--out", t.TempDir(), "--fail-safe", "maybe"}, 2, "BAD_REQUEST"},
		{"missing out", []string{"select", "--diff", "../../fixtures/basic/change.patch", "--coverage", "../../fixtures/basic/coverage"}, 2, "BAD_REQUEST"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var code int
			out := captureStdout(t, func() {
				code, _, _, _ = run(c.args)
			})
			if code != c.code {
				t.Errorf("code=%d, want %d (out=%s)", code, c.code, out)
			}
			var obj map[string]any
			if err := json.Unmarshal([]byte(out), &obj); err != nil {
				t.Fatalf("error output is not JSON: %v", err)
			}
			if errObj, ok := obj["error"].(map[string]any); !ok || errObj["code"] != c.err {
				t.Errorf("error code = %v, want %s", obj["error"], c.err)
			}
		})
	}
}

func TestRunRecordAppends(t *testing.T) {
	outDir := filepath.Join(t.TempDir(), "out")
	var code int
	out := captureStdout(t, func() {
		code, _, _, _ = run([]string{"record", "--missed", "tests/test_a.py::test_y", "--out", outDir, "--json"})
	})
	if code != 0 {
		t.Fatalf("record: code=%d out=%s", code, out)
	}
	out = captureStdout(t, func() {
		code, _, _, _ = run([]string{"record", "--missed", "tests/test_b.py::test_z", "--out", outDir, "--json"})
	})
	if code != 0 {
		t.Fatalf("second record: code=%d", code)
	}
	rows, err := os.ReadFile(filepath.Join(outDir, "missed.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(rows)), "\n")
	if len(lines) != 2 {
		t.Fatalf("missed.jsonl has %d lines, want 2", len(lines))
	}
	for _, line := range lines {
		var row map[string]any
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			t.Fatalf("row is not JSON: %v", err)
		}
		if row["reason"] != "missed" {
			t.Errorf("row = %v", row)
		}
	}
}

func TestRunDoctor(t *testing.T) {
	var code int
	out := captureStdout(t, func() {
		code, _, _, _ = run([]string{"doctor"})
	})
	if code != 0 {
		t.Fatalf("doctor: code=%d out=%s", code, out)
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(out), &obj); err != nil {
		t.Fatalf("doctor output is not JSON: %v", err)
	}
	checks, _ := obj["checks"].([]any)
	if len(checks) != 4 {
		t.Errorf("doctor checks = %d, want 4", len(checks))
	}
}

// TestRunSelfTest builds the real binary (self-test shells out to the
// executable, and the test binary itself is not the CLI) and points the
// CLI at it via osExecutable.
func TestRunSelfTest(t *testing.T) {
	goTool, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go toolchain not on PATH")
	}
	bin := filepath.Join(t.TempDir(), "testpick")
	cmd := exec.Command(goTool, "build", "-o", bin, ".")
	cmd.Dir = "."
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build for self-test: %v (%s)", err, out)
	}
	old := osExecutable
	osExecutable = func() (string, error) { return bin, nil }
	defer func() { osExecutable = old }()
	t.Setenv("TESTPICK_FIXTURES", "../../fixtures")
	var code int
	out := captureStdout(t, func() {
		code, _, _, _ = run([]string{"self-test"})
	})
	if code != 0 {
		t.Fatalf("self-test: code=%d out=%s", code, out)
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(out), &obj); err != nil {
		t.Fatalf("self-test output is not JSON: %v", err)
	}
	cases, _ := obj["cases"].([]any)
	if len(cases) != 5 {
		t.Errorf("self-test cases = %d, want 5", len(cases))
	}
}

func TestRunRequestEnvelope(t *testing.T) {
	dir := t.TempDir()
	req := filepath.Join(dir, "req.json")
	if err := os.WriteFile(req, []byte(`{"protocol":"arabica-plugin-v1","command":"select","args":{"diff":"../../fixtures/basic/change.patch","coverage":"../../fixtures/basic/coverage","out":"`+filepath.Join(dir, "out")+`"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	var code int
	out := captureStdout(t, func() {
		code, _, _, _ = run([]string{"--request", req})
	})
	if code != 0 {
		t.Fatalf("--request envelope: code=%d out=%s", code, out)
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(out), &obj); err != nil {
		t.Fatalf("envelope output is not JSON: %v", err)
	}
	if sel, _ := obj["selected"].([]any); len(sel) != 1 {
		t.Errorf("selected = %v", obj["selected"])
	}
}

func TestSelfTestCommandRunner(t *testing.T) {
	t.Setenv("TESTPICK_SELFTEST_RUNNER", "qemu-aarch64-static")
	cmd := selfTestCommand("/bin/testpick", "select", "--json")
	want := []string{"qemu-aarch64-static", "/bin/testpick", "select", "--json"}
	if len(cmd.Args) != len(want) {
		t.Fatalf("args = %v, want %v", cmd.Args, want)
	}
	for i := range want {
		if cmd.Args[i] != want[i] {
			t.Errorf("args = %v, want %v", cmd.Args, want)
		}
	}
	// Without the runner env, the binary is invoked directly.
	t.Setenv("TESTPICK_SELFTEST_RUNNER", "")
	cmd = selfTestCommand("/bin/testpick", "version")
	if len(cmd.Args) != 2 || cmd.Args[0] != "/bin/testpick" || cmd.Args[1] != "version" {
		t.Errorf("args = %v, want [/bin/testpick version]", cmd.Args)
	}
}

func TestCompareSelection(t *testing.T) {
	want := map[string]any{
		"ok": true, "tool": "testpick", "version": "0.1.0",
		"selected": []any{"a", "b"}, "unmapped": []any{},
		"reason": "r", "fail_safe": "all",
	}
	got := map[string]any{
		"ok": true, "tool": "testpick", "version": "0.1.0",
		"selected": []any{"b", "a"}, "unmapped": []any{},
		"reason": "r", "fail_safe": "all", "extra": "ignored",
	}
	if _, same := compareSelection(want, got); !same {
		t.Error("compareSelection should ignore order and extra fields")
	}
	got["reason"] = "other"
	if _, same := compareSelection(want, got); same {
		t.Error("compareSelection should catch a different reason")
	}
}
