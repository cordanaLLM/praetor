#!/usr/bin/env python3
"""Live Semgrep regressions for the HISS invariant rules."""

import json
import os
import re
import subprocess
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
RULES = ROOT / ".config" / "semgrep" / "hiss-invariants.yml"
VERSION_PROBE_TIMEOUT_SECONDS = 20


def _ensure_semgrep_env():
    semgrep_dir = Path.home() / ".semgrep"
    needs_fallback = (
        ("SEMGREP_SETTINGS_FILE" not in os.environ or "SEMGREP_LOG_FILE" not in os.environ)
        and (
            (semgrep_dir.exists() and not os.access(semgrep_dir, os.W_OK))
            or (not semgrep_dir.exists() and not os.access(Path.home(), os.W_OK))
        )
    )
    if needs_fallback:
        fallback_dir = Path(tempfile.gettempdir()) / "praetor-semgrep"
        fallback_dir.mkdir(parents=True, exist_ok=True)
        os.environ.setdefault("SEMGREP_SETTINGS_FILE", str(fallback_dir / "settings.yaml"))
        os.environ.setdefault("SEMGREP_LOG_FILE", str(fallback_dir / "semgrep.log"))


_ensure_semgrep_env()


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


LOCAL_PARSE_RULE = """\
rules:
  - id: parse-figure-mjs
    pattern: const $X = $Y
    message: parse probe
    languages: [javascript]
    severity: INFO
"""


def tracked_figure_scripts() -> list[Path]:
    result = subprocess.run(
        ["git", "ls-files", "tools/figures/*.mjs"],
        cwd=ROOT,
        capture_output=True,
        text=True,
        check=True,
    )
    paths = [ROOT / line.strip() for line in result.stdout.splitlines() if line.strip()]
    if not paths:
        raise AssertionError("no tracked tools/figures/*.mjs files found")
    return sorted(paths)


def scan_figure_parse_errors(paths: list[Path]) -> list[tuple[str, str, int | None, str]]:
    with tempfile.TemporaryDirectory(prefix="praetor-semgrep-parse-") as directory:
        rule_file = Path(directory) / "parse-probe.yml"
        rule_file.write_text(LOCAL_PARSE_RULE, encoding="utf-8")
        command = [
            "semgrep", "scan",
            "--config", str(rule_file),
            "--metrics=off",
            "--disable-version-check",
            "--no-git-ignore",
            "--json",
            *[str(p) for p in paths],
        ]
        result = subprocess.run(
            command,
            capture_output=True,
            text=True,
            timeout=30,
            check=False,
            cwd=ROOT,
        )
        if result.returncode not in (0, 1):
            raise AssertionError(f"semgrep scan failed ({result.returncode}): {result.stderr}")
        try:
            payload = json.loads(result.stdout)
        except json.JSONDecodeError as error:
            raise AssertionError(f"semgrep returned non-JSON output: {result.stdout!r}") from error

        errors = []
        for err in payload.get("errors", []):
            err_type = err.get("type")
            type_name = str(err_type[0]) if isinstance(err_type, list) and err_type else str(err_type or "")
            path = err.get("path") or ""
            line = None
            spans = err.get("spans") or []
            if spans and isinstance(spans[0], dict) and "start" in spans[0]:
                line = spans[0]["start"].get("line")
                if not path:
                    path = spans[0].get("file", "")
            elif isinstance(err_type, list) and len(err_type) > 1 and isinstance(err_type[1], list) and err_type[1]:
                line = err_type[1][0].get("start", {}).get("line")
                if not path:
                    path = err_type[1][0].get("path", "")
            message = err.get("message") or ""
            lower = type_name.lower()
            if "partialparsing" in lower or "syntax" in lower or "parse" in lower:
                rel_path = path
                try:
                    rel_path = str(Path(path).resolve().relative_to(ROOT))
                except Exception:
                    pass
                errors.append((type_name, rel_path, line, message))
        return errors


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

    def test_tracked_figure_scripts_parse_cleanly_under_semgrep(self):
        scripts = tracked_figure_scripts()
        errors = scan_figure_parse_errors(scripts)
        if errors:
            details = ", ".join(f"{t} at {p}:{line}" for t, p, line, _ in errors)
            self.fail(f"semgrep reported parse errors in tracked figure scripts: {details}")

    def test_semgrep_parse_check_fails_on_planted_regex_literal(self):
        with tempfile.TemporaryDirectory(prefix="praetor-figure-parse-") as directory:
            fixture = Path(directory) / "planted.mjs"
            fixture.write_text(
                "const MAX_ENTRIES = 131_072;\n"
                "const MARKER = /<!-- figure:([a-z0-9-]+) -->\\n([\\s\\S]*?)<!-- \\/figure -->/g;\n",
                encoding="utf-8",
            )
            errors = scan_figure_parse_errors([fixture])
            self.assertTrue(
                any(err[0] == "PartialParsing" for err in errors),
                f"expected PartialParsing error on planted regex literal fixture, got: {errors}",
            )


if __name__ == "__main__":
    unittest.main()
