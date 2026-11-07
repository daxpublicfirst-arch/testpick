#!/usr/bin/env python3
"""Generate large select inputs and time `testpick select` end to end.

This is a development/verification script, not part of the tool. It
builds a synthetic mid-size repository (5,000 tests, 200 files, 20
changed files) and a 200 MiB+ input for the size-limit check, then runs
the binary and prints timings.

Usage:
    python3 scripts/gen_perf_input.py --binary ./bin/testpick --out /tmp/perf
"""

import argparse
import json
import os
import subprocess
import sys
import time


def write_coverage(out_dir, n_tests=5000, n_files=200):
    cov_dir = os.path.join(out_dir, "coverage")
    os.makedirs(cov_dir, exist_ok=True)
    for i in range(n_tests):
        entry = {
            "test": "tests/test_%04d.py::test_x" % i,
            "files": ["src/file%03d.py" % (i % n_files)],
            "lines": list(range(1, 51)),
        }
        with open(os.path.join(cov_dir, "test_%04d.json" % i), "w") as f:
            json.dump(entry, f)
    return cov_dir


def write_diff(out_dir, changed_files=20, lines_per_file=100):
    path = os.path.join(out_dir, "change.patch")
    with open(path, "w") as f:
        for i in range(changed_files):
            f.write("diff --git a/src/file%03d.py b/src/file%03d.py\n" % (i, i))
            f.write("--- a/src/file%03d.py\n" % i)
            f.write("+++ b/src/file%03d.py\n" % i)
            f.write("@@ -1,0 +1,%d @@\n" % lines_per_file)
            for _ in range(lines_per_file):
                f.write("+added line\n")
    return path


def write_oversize_diff(out_dir, target_bytes=205 * 1024 * 1024):
    """A diff just over the 200 MiB input limit, for the INPUT_TOO_LARGE check."""
    path = os.path.join(out_dir, "huge.patch")
    line = "+aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\n"
    with open(path, "w") as f:
        f.write("diff --git a/src/big.py b/src/big.py\n--- a/src/big.py\n+++ b/src/big.py\n@@ -1,0 +1,1 @@\n")
        written = 0
        while written < target_bytes:
            f.write(line)
            written += len(line)
    return path


def run(binary, args):
    start = time.monotonic()
    proc = subprocess.run([binary] + args, capture_output=True, text=True)
    return proc, time.monotonic() - start


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", default="./bin/testpick")
    parser.add_argument("--out", default="/tmp/perf")
    args = parser.parse_args()
    os.makedirs(args.out, exist_ok=True)
    cov_dir = write_coverage(args.out)
    diff = write_diff(args.out)
    print("generated: 5000 coverage files in %s" % cov_dir)
    print("generated: diff %s (%d bytes)" % (diff, os.path.getsize(diff)))

    proc, elapsed = run(args.binary, ["select", "--diff", diff,
                                      "--coverage", cov_dir,
                                      "--out", os.path.join(args.out, "out"),
                                      "--json"])
    print("select exit=%d wall=%.3fs" % (proc.returncode, elapsed))
    out = json.loads(proc.stdout)
    print("selected=%d unmapped=%s reason=%r" % (
        len(out["selected"]), out["unmapped"], out["reason"]))
    if proc.returncode != 0 or len(out["selected"]) != 500:
        print("FAIL: expected exit 0 and 500 selected tests")
        return 1

    huge = write_oversize_diff(args.out)
    print("generated: oversize diff %s (%d bytes)" % (huge, os.path.getsize(huge)))
    proc, elapsed = run(args.binary, ["select", "--diff", huge,
                                      "--coverage", cov_dir,
                                      "--out", os.path.join(args.out, "out2"),
                                      "--json"])
    print("oversize select exit=%d wall=%.3fs" % (proc.returncode, elapsed))
    err = json.loads(proc.stdout)
    print("error=%s" % err["error"]["code"])
    if proc.returncode != 2 or err["error"]["code"] != "INPUT_TOO_LARGE":
        print("FAIL: expected exit 2 and INPUT_TOO_LARGE")
        return 1
    print("PERF OK")
    return 0


if __name__ == "__main__":
    sys.exit(main())
