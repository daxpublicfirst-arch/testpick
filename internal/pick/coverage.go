package pick

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// CoverageEntry is one per-test coverage map file. The schema is
// intentionally small: a test name, the files it reaches, and the lines it
// covers in those files. When Lines is empty or absent the map is
// file-level: the test is taken to cover the whole of every listed file.
//
// NOTE on multi-file maps: Lines applies to every file in Files. A map
// that needs different lines per file must be split into one entry per
// file (or per test/file pair sharing the same test name).
type CoverageEntry struct {
	Test  string   `json:"test"`
	Files []string `json:"files"`
	Lines []int    `json:"lines"`
}

// LoadCoverage reads every *.json file in dir (sorted by file name) and
// parses each as one CoverageEntry. It returns the entries and the total
// number of input bytes read.
//
// Errors use the pick error-code vocabulary so the CLI can fail loudly:
// NO_COVERAGE when the directory is missing or holds no coverage files,
// BAD_COVERAGE when a file is malformed or fails validation.
func LoadCoverage(dir string) ([]CoverageEntry, int64, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, 0, &CodedError{
			Code:    "NO_COVERAGE",
			Blocked: fmt.Sprintf("cannot read coverage directory %q: %v", dir, err),
			Fix:     "Pass --coverage <dir> pointing at a directory of per-test JSON files, one per test.",
			IfFails: "Check the directory exists and is readable, then re-run.",
		}
	}
	var out []CoverageEntry
	var bytes int64
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		path := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, 0, &CodedError{
				Code:    "BAD_COVERAGE",
				Blocked: fmt.Sprintf("cannot read coverage file %q: %v", path, err),
				Fix:     "Make sure every file under --coverage is a readable JSON file.",
				IfFails: "Remove the unreadable file and re-run.",
			}
		}
		bytes += int64(len(data))
		var entry CoverageEntry
		if err := json.Unmarshal(data, &entry); err != nil {
			return nil, 0, &CodedError{
				Code:    "BAD_COVERAGE",
				Blocked: fmt.Sprintf("coverage file %q is not valid JSON: %v", path, err),
				Fix:     `Each coverage file must be one JSON object like {"test":"tests/test_a.py::test_x","files":["src/a.py"],"lines":[10,11,12]}.`,
				IfFails: "Fix the file or regenerate the coverage map, then re-run.",
			}
		}
		if entry.Test == "" || len(entry.Files) == 0 {
			return nil, 0, &CodedError{
				Code:    "BAD_COVERAGE",
				Blocked: fmt.Sprintf("coverage file %q is missing a non-empty \"test\" or \"files\" field", path),
				Fix:     `Each coverage file must name its test and at least one file, for example {"test":"tests/test_a.py::test_x","files":["src/a.py"],"lines":[10,11,12]}.`,
				IfFails: "Fix the file or regenerate the coverage map, then re-run.",
			}
		}
		out = append(out, entry)
	}
	if len(out) == 0 {
		return nil, 0, &CodedError{
			Code:    "NO_COVERAGE",
			Blocked: fmt.Sprintf("no .json coverage files found in %q", dir),
			Fix:     "Point --coverage at a directory containing one JSON file per test.",
			IfFails: "Generate the per-test coverage map first, then re-run.",
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Test < out[j].Test })
	return out, bytes, nil
}

// CodedError is a failure the CLI reports as a single JSON object with a
// machine-readable code and human-readable remediation.
type CodedError struct {
	Code    string
	Blocked string
	Fix     string
	IfFails string
}

func (e *CodedError) Error() string { return e.Code + ": " + e.Blocked }

// ErrorObject renders a CodedError (or any error) as the failure JSON
// object the CLI prints: {"ok":false,"error":{...}}.
func ErrorObject(err error) map[string]any {
	code, blocked, fix, ifFails := "TOOL_ERROR", err.Error(),
		"Re-run with the same inputs and check the error message.",
		"If the problem persists, report the input files and the error above."
	if ce, ok := err.(*CodedError); ok {
		code, blocked, fix, ifFails = ce.Code, ce.Blocked, ce.Fix, ce.IfFails
	}
	return map[string]any{
		"ok": false,
		"error": map[string]any{
			"code":          code,
			"blocked":       blocked,
			"fix":           fix,
			"if_that_fails": ifFails,
		},
	}
}
