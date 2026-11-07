#!/usr/bin/env python3
"""Mock harness: exercise testpick exactly the way a thin adapter would.

The real Arabica harness is not available in this environment, so this
script plays its part. It calls the CLI the way the plugin contract
says an adapter does — flags and the --request envelope — and checks
every promise in plugin.toml and schema/:

* one JSON object on stdout per command, matching schema/response.json
  (for select) and schema/error.json (for failures);
* exit codes 0/1/2/3 with the documented meanings;
* one JSON telemetry line on stderr per run, matching schema/telemetry.json;
* deterministic output (two identical runs give byte-identical stdout);
* --out files agree with stdout;
* record appends a valid JSONL row.

Stdlib only. No network.

Usage:
    python3 examples/mock-harness.py [--binary ./bin/testpick] [--root .]
"""

import argparse
import json
import os
import subprocess
import sys
import tempfile

try:
    import tomllib
except ModuleNotFoundError:  # Python < 3.11
    import tomli as tomllib


class Failure(Exception):
    pass


class Harness:
    def __init__(self, binary, root):
        self.binary = os.path.abspath(binary)
        self.root = os.path.abspath(root)
        self.checks = []

    def check(self, name, ok, detail=""):
        self.checks.append((name, bool(ok), detail))
        print(("PASS " if ok else "FAIL ") + name + ((" — " + detail) if detail and not ok else ""))

    def run_cli(self, args, env=None):
        proc = subprocess.run([self.binary] + args, capture_output=True,
                              text=True, env=env)
        return proc

    def parse_stdout(self, proc, name):
        try:
            obj = json.loads(proc.stdout)
        except json.JSONDecodeError as e:
            raise Failure("%s: stdout is not one JSON object: %s" % (name, e))
        if proc.stdout.count("\n") != 1:
            raise Failure("%s: stdout must be exactly one line" % name)
        return obj

    def check_telemetry(self, proc, name, command, expect_ok):
        lines = [l for l in proc.stderr.splitlines() if l.strip()]
        if len(lines) != 1:
            raise Failure("%s: expected exactly 1 telemetry line, got %d" % (name, len(lines)))
        try:
            tel = json.loads(lines[0])
        except json.JSONDecodeError as e:
            raise Failure("%s: telemetry is not JSON: %s" % (name, e))
        for key in ("tool", "version", "command", "duration_ms",
                    "input_bytes", "output_bytes", "ok"):
            if key not in tel:
                raise Failure("%s: telemetry missing %r" % (name, key))
        if tel["tool"] != "testpick" or tel["version"] != "0.1.0":
            raise Failure("%s: telemetry tool/version wrong: %s" % (name, tel))
        if tel["command"] != command:
            raise Failure("%s: telemetry command %r, want %r" % (name, tel["command"], command))
        if tel["ok"] != expect_ok:
            raise Failure("%s: telemetry ok=%r, want %r" % (name, tel["ok"], expect_ok))
        if tel["duration_ms"] < 0 or tel["input_bytes"] < 0 or tel["output_bytes"] < 0:
            raise Failure("%s: telemetry has negative sizes" % name)

    def check_select_response(self, obj, name, selected, unmapped, fail_safe):
        for key in ("ok", "tool", "version", "selected", "unmapped", "reason", "fail_safe"):
            if key not in obj:
                raise Failure("%s: response missing %r" % (name, key))
        if obj["ok"] is not True or obj["tool"] != "testpick" or obj["version"] != "0.1.0":
            raise Failure("%s: response header wrong: %s" % (name, obj))
        if obj["selected"] != sorted(set(obj["selected"])):
            raise Failure("%s: selected is not sorted/deduplicated" % name)
        if obj["selected"] != selected or obj["unmapped"] != unmapped:
            raise Failure("%s: selected=%r unmapped=%r" % (name, obj["selected"], obj["unmapped"]))
        if obj["fail_safe"] != fail_safe:
            raise Failure("%s: fail_safe=%r, want %r" % (name, obj["fail_safe"], fail_safe))

    def run_all(self):
        with open(os.path.join(self.root, "plugin.toml"), "rb") as f:
            plugin = tomllib.load(f)
        self.check("plugin.toml parses, name/version/protocol",
                   plugin["name"] == "testpick" and plugin["version"] == "0.1.0"
                   and plugin["protocol"] == "arabica-plugin-v1",
                   str(plugin))
        self.check("plugin.toml commands",
                   plugin["commands"] == ["select", "record", "self-test", "doctor", "version"],
                   str(plugin["commands"]))
        self.check("plugin.toml permissions deny network",
                   plugin["permissions"]["network"] is False)

        fixtures = os.path.join(self.root, "fixtures")
        tmp = tempfile.mkdtemp(prefix="mock-harness-")
        try:
            # version (plain and --json)
            proc = self.run_cli(["version"])
            self.check("version prints 'testpick 0.1.0', exit 0",
                       proc.returncode == 0 and proc.stdout == "testpick 0.1.0\n", proc.stdout)
            self.check_telemetry(proc, "version telemetry", "version", True)
            proc = self.run_cli(["version", "--json"])
            obj = self.parse_stdout(proc, "version --json")
            self.check("version --json", proc.returncode == 0 and obj.get("version") == "0.1.0")

            # doctor
            proc = self.run_cli(["doctor"])
            obj = self.parse_stdout(proc, "doctor")
            self.check("doctor exit 0, all checks ok",
                       proc.returncode == 0 and obj.get("ok") is True
                       and all(c["ok"] for c in obj.get("checks", [])), proc.stdout)
            self.check_telemetry(proc, "doctor telemetry", "doctor", True)

            # self-test
            proc = self.run_cli(["self-test"])
            obj = self.parse_stdout(proc, "self-test")
            self.check("self-test exit 0, 5/5 cases",
                       proc.returncode == 0 and obj.get("ok") is True
                       and len(obj.get("cases", [])) == 5
                       and all(c["ok"] for c in obj["cases"]), proc.stdout)

            # select via flags (basic fixture)
            out1 = os.path.join(tmp, "out1")
            proc = self.run_cli(["select", "--diff", os.path.join(fixtures, "basic", "change.patch"),
                                 "--coverage", os.path.join(fixtures, "basic", "coverage"),
                                 "--out", out1, "--json"])
            obj = self.parse_stdout(proc, "select basic")
            self.check_select_response(obj, "select basic",
                                       ["tests/test_a.py::test_x"], [], "all")
            self.check("select basic exit 0", proc.returncode == 0)
            self.check_telemetry(proc, "select telemetry", "select", True)
            with open(os.path.join(out1, "selection.json")) as f:
                self.check("selection.json matches stdout",
                           f.read().strip() == proc.stdout.strip())
            with open(os.path.join(out1, "selection.txt")) as f:
                self.check("selection.txt lists the test",
                           f.read() == "tests/test_a.py::test_x\n")

            # determinism: run select again, stdout must be byte-identical
            proc2 = self.run_cli(["select", "--diff", os.path.join(fixtures, "basic", "change.patch"),
                                  "--coverage", os.path.join(fixtures, "basic", "coverage"),
                                  "--out", os.path.join(tmp, "out1b"), "--json"])
            self.check("select is deterministic (identical stdout twice)",
                       proc2.stdout == proc.stdout)

            # select via --request envelope
            req = os.path.join(tmp, "req.json")
            with open(req, "w") as f:
                json.dump({"protocol": "arabica-plugin-v1", "command": "select",
                           "args": {"diff": os.path.join(fixtures, "basic", "change.patch"),
                                    "coverage": os.path.join(fixtures, "basic", "coverage"),
                                    "out": os.path.join(tmp, "out2")}}, f)
            proc = self.run_cli(["--request", req])
            obj = self.parse_stdout(proc, "select via envelope")
            self.check("select via --request envelope, exit 0",
                       proc.returncode == 0 and obj["selected"] == ["tests/test_a.py::test_x"])

            # unmapped: fail-safe all (exit 0) and none (exit 1)
            proc = self.run_cli(["select", "--diff", os.path.join(fixtures, "unmapped", "change.patch"),
                                 "--coverage", os.path.join(fixtures, "unmapped", "coverage"),
                                 "--out", os.path.join(tmp, "out3"), "--fail-safe", "all", "--json"])
            obj = self.parse_stdout(proc, "unmapped all")
            self.check_select_response(obj, "unmapped all",
                                       ["tests/test_a.py::test_x", "tests/test_b.py::test_y"],
                                       ["src/c.py"], "all")
            self.check("unmapped + fail-safe all exits 0", proc.returncode == 0)
            proc = self.run_cli(["select", "--diff", os.path.join(fixtures, "unmapped", "change.patch"),
                                 "--coverage", os.path.join(fixtures, "unmapped", "coverage"),
                                 "--out", os.path.join(tmp, "out4"), "--fail-safe", "none", "--json"])
            obj = self.parse_stdout(proc, "unmapped none")
            self.check_select_response(obj, "unmapped none", [], ["src/c.py"], "none")
            self.check("unmapped + fail-safe none exits 1", proc.returncode == 1)

            # record
            out5 = os.path.join(tmp, "out5")
            proc = self.run_cli(["record", "--missed", "tests/test_a.py::test_y",
                                 "--out", out5, "--json"])
            obj = self.parse_stdout(proc, "record")
            self.check("record exit 0", proc.returncode == 0 and obj.get("ok") is True)
            with open(os.path.join(out5, "missed.jsonl")) as f:
                row = json.loads(f.read().strip())
            self.check("record appends a valid JSONL row",
                       row["test"] == "tests/test_a.py::test_y" and row["reason"] == "missed")

            # failure: missing diff -> exit 2, error object shape
            proc = self.run_cli(["select", "--diff", os.path.join(tmp, "nope.patch"),
                                 "--coverage", os.path.join(fixtures, "basic", "coverage"),
                                 "--out", os.path.join(tmp, "out6"), "--json"])
            obj = self.parse_stdout(proc, "missing diff")
            err = obj.get("error", {})
            self.check("missing diff exits 2 with NO_DIFF + fix guidance",
                       proc.returncode == 2 and obj.get("ok") is False
                       and err.get("code") == "NO_DIFF" and "fix" in err
                       and "if_that_fails" in err, proc.stdout)
            self.check_telemetry(proc, "error telemetry", "select", False)
        finally:
            pass
        failed = [c for c in self.checks if not c[1]]
        print("\n%d checks, %d passed, %d failed" % (
            len(self.checks), len(self.checks) - len(failed), len(failed)))
        return 1 if failed else 0


def main():
    parser = argparse.ArgumentParser(description="Mock Arabica harness for testpick.")
    parser.add_argument("--binary", default="./bin/testpick")
    parser.add_argument("--root", default=".")
    args = parser.parse_args()
    try:
        return Harness(args.binary, args.root).run_all()
    except Failure as e:
        print("FAIL " + str(e))
        return 1


if __name__ == "__main__":
    sys.exit(main())
