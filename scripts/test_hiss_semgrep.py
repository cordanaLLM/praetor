#!/usr/bin/env python3
"""Live Semgrep regressions for the HISS invariant rules."""

import json
import re
import subprocess
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
RULES = ROOT / ".config" / "semgrep" / "hiss-invariants.yml"
VERSION_PROBE_TIMEOUT_SECONDS = 20


def pinned_version():
    requirements = ROOT / ".config" / "semgrep" / "requirements.txt"
    match = re.search(
        r"^semgrep==([^\s#]+)$",
        requirements.read_text(encoding="utf-8"),
        re.MULTILINE,
    )
    if not match:
        raise AssertionError(f"missing exact semgrep pin in {requirements}")
    return match.group(1)


def require_semgrep_version(run_command=subprocess.run):
    expected = pinned_version()
    command = ["semgrep", "--version"]
    requirement = f"semgrep {expected} is required by requirements.txt"
    try:
        result = run_command(
            command,
            capture_output=True,
            text=True,
            timeout=VERSION_PROBE_TIMEOUT_SECONDS,
            check=False,
        )
    except subprocess.TimeoutExpired as error:
        raise AssertionError(
            f"{requirement}; installed version is unavailable: version probe exceeded "
            f"the {VERSION_PROBE_TIMEOUT_SECONDS}-second bound before this host completed it"
        ) from error

    output = result.stdout.strip()
    installed = output.split()[-1] if result.returncode == 0 and output else ""
    if installed == expected:
        return installed
    if result.returncode != 0:
        detail = f"version probe exited with status {result.returncode}"
    elif not output:
        detail = "version probe returned empty stdout"
    else:
        detail = f"installed version is {installed}"
    if installed:
        raise AssertionError(f"{requirement}; {detail}")
    raise AssertionError(f"{requirement}; installed version is unavailable: {detail}")


def scan(source: str, language: str):
    suffix = {
        "go": ".go",
        "rust": ".rs",
        "python": ".py",
        "javascript": ".js",
        "typescript": ".ts",
        "c": ".c",
        "cpp": ".cpp",
    }[language]
    with tempfile.TemporaryDirectory(prefix="praetor-hiss-") as directory:
        fixture = Path(directory) / f"fixture{suffix}"
        fixture.write_text(source, encoding="utf-8")
        command = [
            "semgrep", "scan", "--config", str(RULES), "--metrics=off",
            "--disable-version-check", "--no-git-ignore", "--json", str(fixture),
        ]
        result = subprocess.run(command, capture_output=True, text=True, timeout=20, check=False)
        if result.returncode not in (0, 1):
            raise AssertionError(f"semgrep failed ({result.returncode}): {result.stderr}")
        try:
            payload = json.loads(result.stdout)
        except json.JSONDecodeError as error:
            raise AssertionError(f"semgrep returned non-JSON output: {result.stdout!r}") from error
        if payload.get("errors"):
            raise AssertionError(f"semgrep reported errors: {payload['errors']}")
        scanned = [Path(path).resolve() for path in payload.get("paths", {}).get("scanned", [])]
        if fixture.resolve() not in scanned:
            raise AssertionError(f"fixture was not scanned: {fixture} not in {scanned}")
        return [
            {"rule": item["check_id"].rsplit(".", 1)[-1], "line": item["start"]["line"]}
            for item in payload.get("results", [])
        ]


def rules_at(findings, rule):
    return [item["line"] for item in findings if item["rule"] == rule]


class SemgrepVersionProbeTest(unittest.TestCase):
    def test_matching_version_passes_with_scan_sized_timeout(self):
        calls = []

        def run_command(command, **kwargs):
            calls.append((command, kwargs))
            return subprocess.CompletedProcess(command, 0, f"semgrep {pinned_version()}\n", "")

        self.assertEqual(require_semgrep_version(run_command), pinned_version())
        self.assertEqual(calls[0][0], ["semgrep", "--version"])
        self.assertEqual(calls[0][1]["timeout"], 20)

    def test_mismatched_version_names_expected_and_installed_versions(self):
        def run_command(command, **_kwargs):
            return subprocess.CompletedProcess(command, 0, "semgrep 0.0.0\n", "")

        with self.assertRaisesRegex(
            AssertionError,
            rf"semgrep {re.escape(pinned_version())} is required.*installed version is 0\.0\.0",
        ):
            require_semgrep_version(run_command)

    def test_timeout_names_bound_and_host_instead_of_toolchain_traceback(self):
        def run_command(command, **kwargs):
            raise subprocess.TimeoutExpired(command, kwargs["timeout"])

        with self.assertRaisesRegex(
            AssertionError,
            r"installed version is unavailable: version probe exceeded the 20-second "
            r"bound before this host completed it",
        ):
            require_semgrep_version(run_command)

    def test_empty_stdout_reports_unavailable_version(self):
        def run_command(command, **_kwargs):
            return subprocess.CompletedProcess(command, 0, "", "")

        with self.assertRaisesRegex(
            AssertionError,
            r"installed version is unavailable: version probe returned empty stdout",
        ):
            require_semgrep_version(run_command)


class HissSemgrepRegression(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        require_semgrep_version()

    def test_http_get_and_post_each_match_but_context_request_does_not(self):
        for call in ('http.Get("x")', 'http.Post("x", "text/plain", nil)'):
            with self.subTest(call=call):
                source = 'package main\nimport "net/http"\nfunc bad() { ' + call + ' }\n'
                self.assertEqual(
                    rules_at(scan(source, "go"), "hiss-02-go-http-without-context"), [3]
                )
        safe = (
            'package main\nimport ("context"; "net/http")\n'
            'func good(ctx context.Context) { req, _ := http.NewRequestWithContext('
            'ctx, http.MethodGet, "x", nil); http.DefaultClient.Do(req) }\n'
        )
        self.assertEqual(
            rules_at(scan(safe, "go"), "hiss-02-go-http-without-context"), []
        )

    def test_eval_is_banned_and_non_eval_is_allowed_per_language(self):
        cases = {
            "python": ("eval(user_input)\n", "value = user_input\n", "hiss-08-banned-eval-python"),
            "javascript": ("eval(user_input);\n", "const value = user_input;\n", "hiss-08-banned-eval"),
            "typescript": ("eval(user_input);\n", "const value: string = user_input;\n", "hiss-08-banned-eval"),
        }
        for language, (bad, good, rule) in cases.items():
            with self.subTest(language=language):
                self.assertEqual(rules_at(scan(bad, language), rule), [1])
                self.assertEqual(rules_at(scan(good, language), rule), [])

    def test_rust_production_calls_match_and_test_code_is_exempt(self):
        source = (
            'fn production(v: Option<i32>) { let _ = v.unwrap(); '
            'let _ = v.expect("required"); }\n'
            '#[cfg(test)]\nmod fixtures { fn allowed(v: Option<i32>) { '
            'let _ = v.unwrap(); let _ = v.expect("fixture"); } }\n'
            'fn after_fixtures(v: Option<i32>) { let _ = v.unwrap(); '
            'let _ = v.expect("required"); }\n'
        )
        findings = scan(source, "rust")
        self.assertEqual(rules_at(findings, "hiss-07-banned-unwrap"), [1, 4])
        self.assertEqual(rules_at(findings, "hiss-07-banned-expect"), [1, 4])
        standalone = '#[cfg(test)]\nfn allowed(v: Option<i32>) { let _ = v.unwrap(); let _ = v.expect("fixture"); }\n'
        self.assertEqual(scan(standalone, "rust"), [])
        # Anonymous module matching must depend on cfg(test), not on any module
        # name or the presence of a nearby attribute.
        body = 'fn helper(v: Option<i32>) { v.unwrap(); v.expect("fixture"); }'
        for prefix in ("", "#[cfg(not(test))]\n"):
            with self.subTest(prefix=prefix):
                findings = scan(prefix + 'mod production { ' + body + ' }\n', "rust")
                line = 2 if prefix else 1
                self.assertEqual(rules_at(findings, "hiss-07-banned-unwrap"), [line])
                self.assertEqual(rules_at(findings, "hiss-07-banned-expect"), [line])
        for name in ("fixtures", "tests"):
            self.assertEqual(scan('#[cfg(test)]\nmod ' + name + ' { ' + body + ' }\n', "rust"), [])

    def test_c_and_cpp_each_ban_legacy_calls_and_allow_bounded_calls(self):
        for language in ("c", "cpp"):
            for call in ("gets(a)", "strcpy(a, b)", 'sprintf(a, "%s", b)'):
                with self.subTest(language=language, call=call):
                    source = "void bad(char *a, const char *b) { " + call + "; }\n"
                    self.assertEqual(
                        rules_at(scan(source, language), "hiss-08-banned-c-memory"), [1]
                    )
            bounded = (
                'void good(char *a, const char *b) { fgets(a, 8, stdin); '
                'strncpy(a, b, 7); snprintf(a, 8, "%s", b); }\n'
            )
            self.assertEqual(
                rules_at(scan(bounded, language), "hiss-08-banned-c-memory"), []
            )


if __name__ == "__main__":
    unittest.main()
