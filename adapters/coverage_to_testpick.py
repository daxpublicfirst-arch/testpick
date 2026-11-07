#!/usr/bin/env python3
"""Convert coverage tool output into testpick's small per-test schema.

testpick's core reads only this shape, one JSON file per test::

    {"test": "tests/test_a.py::test_x", "files": ["src/a.py"], "lines": [10, 11, 12]}

A coverage map with no ``lines`` is file-level: the test is taken to cover
the whole of every listed file.

This adapter converts the output of tools that already collect coverage
instead of reimplementing collection:

* ``coverage.py`` JSON (``coverage json -o coverage.json``) -> one entry
  per file set, labelled with ``--test``. coverage.py reports whole-suite
  coverage, not per-test coverage; run it once per test (or once per
  test file) to get a per-test map.
* ``go test -coverprofile`` (``go test -coverprofile=cover.out ./...``)
  -> one entry labelled with ``--test``. The profile is also whole-suite;
  run it per package/test to get a per-test map.

No network. Reads local files only. Writes only under ``--out``.

Usage:
    python3 adapters/coverage_to_testpick.py coveragepy coverage.json --test tests/test_a.py::test_x --out coverage/
    python3 adapters/coverage_to_testpick.py gocover cover.out --test pkg/foo.TestX --out coverage/
"""

import argparse
import json
import os
import re
import sys

SCHEMA_VERSION = "0.1.0"


def write_entry(out_dir, test, files_lines):
    """Write one JSON file per (test, file-group) entry.

    files_lines is a dict of path -> sorted list of covered lines.
    Files with no line detail are written with lines omitted.
    """
    os.makedirs(out_dir, exist_ok=True)
    files = sorted(files_lines)
    lines = sorted({n for ns in files_lines.values() for n in ns})
    entry = {"test": test, "files": files}
    if lines:
        entry["lines"] = lines
    # One file per test; the name is the test name made filename-safe.
    safe = re.sub(r"[^A-Za-z0-9_.-]+", "_", test).strip("_") or "test"
    path = os.path.join(out_dir, safe + ".json")
    with open(path, "w", encoding="utf-8") as f:
        json.dump(entry, f, sort_keys=True)
        f.write("\n")
    return path


def convert_coveragepy(path, test):
    """Parse a coverage.py JSON report into {file: [executed lines]}."""
    with open(path, encoding="utf-8") as f:
        data = json.load(f)
    files = data.get("files", {})
    if not isinstance(files, dict) or not files:
        raise SystemExit("coveragepy: no 'files' object found in %s" % path)
    out = {}
    for name, info in files.items():
        executed = info.get("executed_lines") or []
        if executed:
            out[name] = sorted(set(int(n) for n in executed))
    if not out:
        raise SystemExit("coveragepy: no executed lines found in %s" % path)
    return out


def convert_gocover(path, test):
    """Parse a go coverprofile into {file: [covered lines]}.

    Profile lines look like: ``src/a.go:10.20,12.5 1 1`` — the last
    number is the execution count; a block counts as covered when it is
    non-zero. Lines are expanded from the block's start/end lines.
    """
    out = {}
    with open(path, encoding="utf-8") as f:
        first = f.readline()
        if not first.startswith("mode:"):
            raise SystemExit("gocover: %s is not a go coverprofile (first line must start with 'mode:')" % path)
        for line in f:
            line = line.strip()
            if not line:
                continue
            m = re.match(r"^(.+):(\d+)\.\d+,(\d+)\.\d+ \d+ (\d+)$", line)
            if not m:
                raise SystemExit("gocover: cannot parse profile line: %r" % line)
            name, start, end, count = m.group(1), int(m.group(2)), int(m.group(3)), int(m.group(4))
            if count == 0:
                continue
            out.setdefault(name, set()).update(range(start, end + 1))
    if not out:
        raise SystemExit("gocover: no covered lines found in %s" % path)
    return {k: sorted(v) for k, v in out.items()}


def main(argv=None):
    parser = argparse.ArgumentParser(
        description="Convert coverage tool output to testpick's per-test schema."
    )
    parser.add_argument("kind", choices=["coveragepy", "gocover"],
                        help="input format")
    parser.add_argument("input", help="coverage file to read")
    parser.add_argument("--test", required=True,
                        help="test name for the generated entry, e.g. tests/test_a.py::test_x")
    parser.add_argument("--out", required=True,
                        help="directory to write the per-test JSON file into")
    args = parser.parse_args(argv)
    if args.kind == "coveragepy":
        files_lines = convert_coveragepy(args.input, args.test)
    else:
        files_lines = convert_gocover(args.input, args.test)
    path = write_entry(args.out, args.test, files_lines)
    print(json.dumps({"ok": True, "tool": "testpick-adapter",
                      "version": SCHEMA_VERSION, "wrote": path}))
    return 0


if __name__ == "__main__":
    sys.exit(main())
