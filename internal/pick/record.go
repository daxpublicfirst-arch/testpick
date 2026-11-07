package pick

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// RecordRow is one row of the missed-test log. It is a record for later
// analysis; recording a miss never changes selection.
type RecordRow struct {
	Test   string `json:"test"`
	At     string `json:"at"`
	Reason string `json:"reason"`
}

// MissedFile is the file under --out that record appends to.
const MissedFile = "missed.jsonl"

// Record appends a row for a missed test to <outDir>/missed.jsonl,
// creating the directory when needed. It returns the row and the path
// written. The timestamp is UTC RFC 3339.
func Record(outDir, test string, now time.Time) (RecordRow, string, error) {
	if test == "" {
		return RecordRow{}, "", &CodedError{
			Code:    "BAD_REQUEST",
			Blocked: "record requires a non-empty --missed <test> value",
			Fix:     "Pass the test that was missed, for example: testpick record --missed tests/test_a.py::test_y --out ./out",
			IfFails: "Check the command line and re-run.",
		}
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return RecordRow{}, "", &CodedError{
			Code:    "IO_ERROR",
			Blocked: fmt.Sprintf("cannot create output directory %q: %v", outDir, err),
			Fix:     "Pass an --out directory that can be created and written to.",
			IfFails: "Check permissions on the parent directory and re-run.",
		}
	}
	row := RecordRow{Test: test, At: now.UTC().Format(time.RFC3339), Reason: "missed"}
	data, err := json.Marshal(row)
	if err != nil {
		return RecordRow{}, "", err
	}
	path := filepath.Join(outDir, MissedFile)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return RecordRow{}, "", &CodedError{
			Code:    "IO_ERROR",
			Blocked: fmt.Sprintf("cannot append to %q: %v", path, err),
			Fix:     "Pass an --out directory that can be written to.",
			IfFails: "Check permissions on the output directory and re-run.",
		}
	}
	defer f.Close()
	if _, err := f.Write(append(data, '\n')); err != nil {
		return RecordRow{}, "", &CodedError{
			Code:    "IO_ERROR",
			Blocked: fmt.Sprintf("cannot write to %q: %v", path, err),
			Fix:     "Check that the output disk is not full, then re-run.",
			IfFails: "Try a different --out directory.",
		}
	}
	return row, path, nil
}
