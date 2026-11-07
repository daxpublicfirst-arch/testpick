package pick

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Change is the set of changed lines per file, parsed from a unified diff.
// Keys are repository-relative paths as they appear after the b/ prefix
// (for example "src/a.py"). Values are the added (new-side) line numbers.
type Change struct {
	Files map[string]map[int]bool
}

// ChangedFiles returns the changed file paths, sorted.
func (c *Change) ChangedFiles() []string {
	out := make([]string, 0, len(c.Files))
	for f := range c.Files {
		out = append(out, f)
	}
	sort.Strings(out)
	return out
}

// ChangedLines returns the changed line numbers for a file, sorted.
func (c *Change) ChangedLines(path string) []int {
	set, ok := c.Files[path]
	if !ok {
		return nil
	}
	out := make([]int, 0, len(set))
	for n := range set {
		out = append(out, n)
	}
	sort.Ints(out)
	return out
}

// hunkHeader matches "@@ -a,b +c,d @@". The ",b" / ",d" counts are optional
// (a count of 1 may be written without a comma).
var hunkHeader = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)

// ParseDiff parses a unified diff as produced by `git diff`,
// `git diff --no-index`, or `diff -u`.
//
//   - The changed path comes from the `+++ b/<path>` line. A `+++ /dev/null`
//     line means the file was deleted and contributes no changed lines.
//   - Each added line ('+' inside a hunk) is a changed line at the current
//     new-side line number. Removed lines are ignored. Context lines advance
//     the new-side counter without recording a change.
//   - Lines outside any hunk are ignored.
//
// It returns an error only when a hunk header is malformed; a diff with no
// hunks is a valid empty change.
func ParseDiff(data []byte) (*Change, error) {
	ch := &Change{Files: map[string]map[int]bool{}}
	var curFile string
	var inHunk bool
	var newLine int

	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSuffix(raw, "\r")
		switch {
		case strings.HasPrefix(line, "+++ "):
			path := strings.TrimSpace(strings.TrimPrefix(line, "+++ "))
			// Strip a timestamp tab suffix, as produced by `diff -u`.
			if i := strings.Index(path, "\t"); i >= 0 {
				path = path[:i]
			}
			path = strings.TrimPrefix(path, "b/")
			if path == "/dev/null" || path == "" {
				curFile = ""
			} else {
				curFile = path
				if _, ok := ch.Files[curFile]; !ok {
					ch.Files[curFile] = map[int]bool{}
				}
			}
			inHunk = false
		case strings.HasPrefix(line, "@@ "):
			m := hunkHeader.FindStringSubmatch(line)
			if m == nil {
				return nil, fmt.Errorf("malformed hunk header: %q", line)
			}
			start, err := strconv.Atoi(m[3])
			if err != nil {
				return nil, fmt.Errorf("malformed hunk header: %q", line)
			}
			newLine = start
			inHunk = true
		case inHunk && curFile != "":
			switch {
			case strings.HasPrefix(line, "+"):
				ch.Files[curFile][newLine] = true
				newLine++
			case strings.HasPrefix(line, " "):
				newLine++
			case strings.HasPrefix(line, "-"):
				// Removed line: old side only, new-side counter unchanged.
			case strings.HasPrefix(line, `\`):
				// "\ No newline at end of file": ignore.
			default:
				// Anything else inside a hunk ends it.
				inHunk = false
			}
		}
	}
	return ch, nil
}
