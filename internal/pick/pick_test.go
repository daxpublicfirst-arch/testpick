package pick

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestParseDiffBasic(t *testing.T) {
	data := `diff --git a/src/a.py b/src/a.py
--- a/src/a.py
+++ b/src/a.py
@@ -9,0 +10,3 @@
+line 10
+line 11
+line 12
diff --git a/src/b.py b/src/b.py
--- a/src/b.py
+++ b/src/b.py
@@ -1,4 +1,4 @@
 context
-old line
+new line
 context
`
	ch, err := ParseDiff([]byte(data))
	if err != nil {
		t.Fatalf("ParseDiff: %v", err)
	}
	if got := ch.ChangedLines("src/a.py"); !reflect.DeepEqual(got, []int{10, 11, 12}) {
		t.Errorf("src/a.py changed lines = %v, want [10 11 12]", got)
	}
	if got := ch.ChangedLines("src/b.py"); !reflect.DeepEqual(got, []int{2}) {
		t.Errorf("src/b.py changed lines = %v, want [2]", got)
	}
}

func TestParseDiffIgnoresRemovedAndDevNull(t *testing.T) {
	data := `diff --git a/src/del.py b/src/del.py
--- a/src/del.py
+++ /dev/null
@@ -1,2 +0,0 @@
-gone 1
-gone 2
`
	ch, err := ParseDiff([]byte(data))
	if err != nil {
		t.Fatalf("ParseDiff: %v", err)
	}
	if len(ch.Files) != 0 {
		t.Errorf("deleted file should contribute no changes, got %v", ch.Files)
	}
}

func TestParseDiffBadHunk(t *testing.T) {
	if _, err := ParseDiff([]byte("+++ b/x.py\n@@ nonsense @@\n+line\n")); err == nil {
		t.Error("expected an error for a malformed hunk header")
	}
}

func TestParseDiffEmpty(t *testing.T) {
	ch, err := ParseDiff([]byte(""))
	if err != nil {
		t.Fatalf("ParseDiff: %v", err)
	}
	if len(ch.Files) != 0 {
		t.Errorf("empty diff should have no changed files, got %v", ch.Files)
	}
}

func covEntry(test string, files []string, lines []int) CoverageEntry {
	return CoverageEntry{Test: test, Files: files, Lines: lines}
}

func TestSelectExactMatch(t *testing.T) {
	ch, _ := ParseDiff([]byte("+++ b/src/a.py\n@@ -9,0 +10,3 @@\n+x\n+y\n+z\n"))
	cov := []CoverageEntry{
		covEntry("tests/test_a.py::test_x", []string{"src/a.py"}, []int{10, 11, 12}),
		covEntry("tests/test_b.py::test_y", []string{"src/b.py"}, []int{1}),
	}
	sel := Select(ch, cov, FailSafeAll)
	if !reflect.DeepEqual(sel.Selected, []string{"tests/test_a.py::test_x"}) {
		t.Errorf("selected = %v", sel.Selected)
	}
	if len(sel.Unmapped) != 0 {
		t.Errorf("unmapped = %v, want empty", sel.Unmapped)
	}
	if sel.ExitCode() != 0 {
		t.Errorf("exit code = %d, want 0", sel.ExitCode())
	}
}

func TestSelectLineMismatchNotSelected(t *testing.T) {
	ch, _ := ParseDiff([]byte("+++ b/src/a.py\n@@ -9,0 +10,3 @@\n+x\n+y\n+z\n"))
	cov := []CoverageEntry{
		covEntry("tests/test_a.py::test_x", []string{"src/a.py"}, []int{99}),
	}
	sel := Select(ch, cov, FailSafeAll)
	if len(sel.Selected) != 0 {
		t.Errorf("selected = %v, want empty: line-level coverage must match the changed line", sel.Selected)
	}
	if sel.Reason != "no test covers the changed lines" {
		t.Errorf("reason = %q", sel.Reason)
	}
	if sel.ExitCode() != 1 {
		t.Errorf("exit code = %d, want 1", sel.ExitCode())
	}
}

func TestSelectFileLevelMap(t *testing.T) {
	ch, _ := ParseDiff([]byte("+++ b/src/a.py\n@@ -9,0 +10,3 @@\n+x\n+y\n+z\n"))
	cov := []CoverageEntry{
		covEntry("tests/test_a.py::test_x", []string{"src/a.py"}, nil),
	}
	sel := Select(ch, cov, FailSafeAll)
	if !reflect.DeepEqual(sel.Selected, []string{"tests/test_a.py::test_x"}) {
		t.Errorf("file-level map should select the test, got %v", sel.Selected)
	}
}

func TestSelectNameMatchAloneIsNotEnough(t *testing.T) {
	ch, _ := ParseDiff([]byte("+++ b/src/a.py\n@@ -9,0 +10,3 @@\n+x\n+y\n+z\n"))
	cov := []CoverageEntry{
		// Test name mentions a.py but its coverage is for another file.
		covEntry("tests/test_a.py::test_x", []string{"src/other.py"}, []int{1}),
	}
	sel := Select(ch, cov, FailSafeNone)
	if len(sel.Selected) != 0 {
		t.Errorf("selected = %v, want empty: name resemblance is not coverage", sel.Selected)
	}
	if !reflect.DeepEqual(sel.Unmapped, []string{"src/a.py"}) {
		t.Errorf("unmapped = %v, want [src/a.py]", sel.Unmapped)
	}
	if sel.ExitCode() != 1 {
		t.Errorf("exit code = %d, want 1", sel.ExitCode())
	}
}

func TestSelectUnmappedFailSafeAll(t *testing.T) {
	ch, _ := ParseDiff([]byte("+++ b/src/c.py\n@@ -4,0 +5,2 @@\n+x\n+y\n"))
	cov := []CoverageEntry{
		covEntry("tests/test_a.py::test_x", []string{"src/a.py"}, []int{1}),
		covEntry("tests/test_b.py::test_y", []string{"src/b.py"}, []int{1}),
	}
	sel := Select(ch, cov, FailSafeAll)
	if !reflect.DeepEqual(sel.Selected, []string{"tests/test_a.py::test_x", "tests/test_b.py::test_y"}) {
		t.Errorf("fail-safe all should select every test, got %v", sel.Selected)
	}
	if sel.Reason != "1 changed file has no coverage" {
		t.Errorf("reason = %q", sel.Reason)
	}
	if sel.ExitCode() != 0 {
		t.Errorf("exit code = %d, want 0", sel.ExitCode())
	}
}

func TestSelectUnmappedFailSafeNone(t *testing.T) {
	ch, _ := ParseDiff([]byte("+++ b/src/c.py\n@@ -4,0 +5,2 @@\n+x\n+y\n"))
	cov := []CoverageEntry{
		covEntry("tests/test_a.py::test_x", []string{"src/a.py"}, []int{1}),
		covEntry("tests/test_b.py::test_y", []string{"src/b.py"}, []int{1}),
	}
	sel := Select(ch, cov, FailSafeNone)
	if len(sel.Selected) != 0 {
		t.Errorf("fail-safe none should select only exact matches, got %v", sel.Selected)
	}
	if sel.ExitCode() != 1 {
		t.Errorf("exit code = %d, want 1", sel.ExitCode())
	}
}

func TestSelectSortedAndDeduped(t *testing.T) {
	ch, _ := ParseDiff([]byte("+++ b/src/a.py\n@@ -9,0 +10,3 @@\n+x\n+y\n+z\n"))
	cov := []CoverageEntry{
		covEntry("tests/test_z.py::test_2", []string{"src/a.py"}, []int{10}),
		covEntry("tests/test_a.py::test_1", []string{"src/a.py"}, []int{11}),
		covEntry("tests/test_a.py::test_1", []string{"src/a.py"}, []int{12}),
	}
	sel := Select(ch, cov, FailSafeAll)
	if !reflect.DeepEqual(sel.Selected, []string{"tests/test_a.py::test_1", "tests/test_z.py::test_2"}) {
		t.Errorf("selected = %v, want sorted and deduplicated", sel.Selected)
	}
}

func TestLoadCoverageErrors(t *testing.T) {
	if _, _, err := LoadCoverage(t.TempDir()); err == nil {
		t.Error("empty coverage dir should fail with NO_COVERAGE")
	} else if ce, ok := err.(*CodedError); !ok || ce.Code != "NO_COVERAGE" {
		t.Errorf("empty dir error = %v, want NO_COVERAGE", err)
	}
	if _, _, err := LoadCoverage(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Error("missing coverage dir should fail")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "bad.json"), []byte("{nope"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadCoverage(dir); err == nil {
		t.Error("malformed coverage JSON should fail")
	} else if ce, ok := err.(*CodedError); !ok || ce.Code != "BAD_COVERAGE" {
		t.Errorf("malformed JSON error = %v, want BAD_COVERAGE", err)
	}
}

func TestRecordAppendsJSONL(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 11, 8, 0, 0, 0, 0, time.UTC)
	row, path, err := Record(dir, "tests/test_a.py::test_y", now)
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	if row.Test != "tests/test_a.py::test_y" || row.At != "2026-11-08T00:00:00Z" || row.Reason != "missed" {
		t.Errorf("row = %+v", row)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "{\"test\":\"tests/test_a.py::test_y\",\"at\":\"2026-11-08T00:00:00Z\",\"reason\":\"missed\"}\n"
	if string(data) != want {
		t.Errorf("missed.jsonl = %q, want %q", string(data), want)
	}
	// A second record appends rather than replacing.
	if _, _, err := Record(dir, "tests/test_b.py::test_z", now); err != nil {
		t.Fatalf("second Record: %v", err)
	}
	data, _ = os.ReadFile(path)
	lines := 0
	for _, b := range data {
		if b == '\n' {
			lines++
		}
	}
	if lines != 2 {
		t.Errorf("missed.jsonl has %d lines, want 2", lines)
	}
}
