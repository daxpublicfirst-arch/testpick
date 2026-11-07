package pick

import (
	"fmt"
	"testing"
)

// largeFixture builds a synthetic large input: nTests coverage entries
// spread over nFiles files, and a diff touching changedFiles of them.
// The numbers mirror a mid-size repository: 5,000 tests, 200 files.
func largeFixture(nTests, nFiles, changedFiles int) (*Change, []CoverageEntry) {
	ch := &Change{Files: map[string]map[int]bool{}}
	for i := 0; i < changedFiles; i++ {
		path := fmt.Sprintf("src/file%03d.py", i)
		lines := map[int]bool{}
		for n := 1; n <= 100; n++ {
			lines[n] = true
		}
		ch.Files[path] = lines
	}
	cov := make([]CoverageEntry, 0, nTests)
	for i := 0; i < nTests; i++ {
		file := fmt.Sprintf("src/file%03d.py", i%nFiles)
		lines := make([]int, 0, 50)
		for n := 1; n <= 50; n++ {
			lines = append(lines, n)
		}
		cov = append(cov, CoverageEntry{
			Test:  fmt.Sprintf("tests/test_%04d.py::test_x", i),
			Files: []string{file},
			Lines: lines,
		})
	}
	return ch, cov
}

func BenchmarkSelectLarge(b *testing.B) {
	ch, cov := largeFixture(5000, 200, 20)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sel := Select(ch, cov, FailSafeAll)
		if len(sel.Selected) == 0 {
			b.Fatal("expected a non-empty selection")
		}
	}
}

func BenchmarkSelectLargeFailSafeNone(b *testing.B) {
	ch, cov := largeFixture(5000, 200, 20)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sel := Select(ch, cov, FailSafeNone)
		if len(sel.Selected) == 0 {
			b.Fatal("expected a non-empty selection")
		}
	}
}

func BenchmarkParseDiffLarge(b *testing.B) {
	// A diff with 10,000 added lines across 100 files.
	var sb []byte
	for f := 0; f < 100; f++ {
		sb = append(sb, []byte(fmt.Sprintf("diff --git a/src/f%03d.py b/src/f%03d.py\n--- a/src/f%03d.py\n+++ b/src/f%03d.py\n@@ -1,0 +1,100 @@\n", f, f, f, f))...)
		for n := 0; n < 100; n++ {
			sb = append(sb, []byte("+added line\n")...)
		}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ch, err := ParseDiff(sb)
		if err != nil {
			b.Fatal(err)
		}
		if len(ch.Files) != 100 {
			b.Fatalf("parsed %d files, want 100", len(ch.Files))
		}
	}
}

// TestSelectLargeCorrectness checks the large fixture selects exactly the
// tests covering the 20 changed files: 5000 tests / 200 files means 25
// tests per file, so 20 * 25 = 500 selected.
func TestSelectLargeCorrectness(t *testing.T) {
	ch, cov := largeFixture(5000, 200, 20)
	sel := Select(ch, cov, FailSafeAll)
	if len(sel.Selected) != 500 {
		t.Errorf("selected %d tests, want 500", len(sel.Selected))
	}
	if len(sel.Unmapped) != 0 {
		t.Errorf("unmapped = %v, want empty", sel.Unmapped)
	}
}
