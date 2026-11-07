package pick

import (
	"fmt"
	"sort"
)

// Selection is the result of selecting tests for a change.
type Selection struct {
	OK       bool     `json:"ok"`
	Tool     string   `json:"tool"`
	Version  string   `json:"version"`
	Selected []string `json:"selected"`
	Unmapped []string `json:"unmapped"`
	Reason   string   `json:"reason"`
	FailSafe string   `json:"fail_safe"`
}

// FailSafeAll selects every test when any changed file has no coverage.
// FailSafeNone keeps only the exact matches.
const (
	FailSafeAll  = "all"
	FailSafeNone = "none"
)

// Select picks the tests that reach the change.
//
// A test is selected when it covers a changed file AND a changed line, or
// when its coverage names the file with no line detail (a file-level map).
// A test is never selected merely because its name resembles a changed
// file; coverage is required.
//
// Unmapped lists changed files that no test covers at all. When unmapped
// is non-empty and failSafe is "all" (the default), Selected includes
// every test in the coverage directory. With failSafe "none", Selected
// stays the exact match and the caller decides what to do.
//
// Selected and Unmapped are sorted and deduplicated.
func Select(ch *Change, cov []CoverageEntry, failSafe string) Selection {
	changed := ch.Files
	selected := map[string]bool{}
	coveredFile := map[string]bool{}

	for _, entry := range cov {
		lineSet := map[int]bool{}
		for _, n := range entry.Lines {
			lineSet[n] = true
		}
		fileLevel := len(entry.Lines) == 0
		for _, f := range entry.Files {
			changedLines, isChanged := changed[f]
			if !isChanged {
				continue
			}
			coveredFile[f] = true
			if fileLevel {
				selected[entry.Test] = true
				continue
			}
			for n := range changedLines {
				if lineSet[n] {
					selected[entry.Test] = true
					break
				}
			}
		}
	}

	unmapped := make([]string, 0)
	for f := range changed {
		if !coveredFile[f] {
			unmapped = append(unmapped, f)
		}
	}
	sort.Strings(unmapped)

	sel := make([]string, 0, len(selected))
	for t := range selected {
		sel = append(sel, t)
	}
	sort.Strings(sel)

	reason := ""
	switch {
	case len(unmapped) > 0:
		if len(unmapped) == 1 {
			reason = "1 changed file has no coverage"
		} else {
			reason = fmt.Sprintf("%d changed files have no coverage", len(unmapped))
		}
		if failSafe == FailSafeAll {
			// Fail safe: run everything rather than silently skip the
			// uncovered change.
			sel = make([]string, 0, len(cov))
			seen := map[string]bool{}
			for _, entry := range cov {
				if !seen[entry.Test] {
					seen[entry.Test] = true
					sel = append(sel, entry.Test)
				}
			}
			sort.Strings(sel)
		}
	case len(sel) > 0:
		if len(sel) == 1 && len(changed) == 1 {
			reason = "1 test selected for 1 changed file"
		} else {
			reason = fmt.Sprintf("%d tests selected for %d changed files", len(sel), len(changed))
		}
	case len(changed) == 0:
		reason = "no changed files"
	default:
		reason = "no test covers the changed lines"
	}

	return Selection{
		OK:       true,
		Tool:     ToolName,
		Version:  Version,
		Selected: sel,
		Unmapped: unmapped,
		Reason:   reason,
		FailSafe: failSafe,
	}
}

// ExitCode maps a selection to the CLI exit code: 0 success, 1 no test
// covers the change (not an error; the caller decides).
//
// Exit 1 when nothing was selected, or when fail-safe is "none" and any
// changed file is unmapped (even if some other change matched exactly).
// With fail-safe "all" an unmapped file selects every test, which is a
// successful (if conservative) selection, so it exits 0.
func (s Selection) ExitCode() int {
	if len(s.Selected) == 0 {
		return 1
	}
	if s.FailSafe == FailSafeNone && len(s.Unmapped) > 0 {
		return 1
	}
	return 0
}
