#!/usr/bin/env python3
"""Run one bounded local repair from retained dogfood reports, or manage its timer."""

import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import signal
import stat
import subprocess
import sys
import tempfile
import time

sys.dont_write_bytecode = True
import dev_schedule as schedule

MAX_ENTRIES = 256
MAX_REPORTS = 64
MAX_REPORT_BYTES = 8 << 20
CONFIG_FIELDS = {"version", "runner_binary", "runner_sha256", "schedule_state_dir",
                 "repair_config", "repair_config_sha256"}


def unique_object(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise ValueError("Duplicate JSON key")
        result[key] = value
    return result


def private_bytes(path, maximum):
    path = schedule.checked_path(path)
    descriptor = os.open(path, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
    with os.fdopen(descriptor, "rb") as stream:
        before = os.fstat(stream.fileno())
        if not stat.S_ISREG(before.st_mode) or before.st_mode & 0o077 or before.st_uid != os.getuid():
            raise ValueError("Repair input must be an owned private regular file")
        if before.st_size > maximum:
            raise ValueError("Repair input exceeds its byte bound")
        data = stream.read(maximum + 1)
        after = os.fstat(stream.fileno())
        if len(data) > maximum or (before.st_size, before.st_mtime_ns, before.st_ctime_ns) != (after.st_size, after.st_mtime_ns, after.st_ctime_ns):
            raise ValueError("Repair input changed or exceeded its bound")
        return data


def configuration(path):
    raw = private_bytes(path, schedule.MAX_FILE)
    config = json.loads(raw, object_pairs_hook=unique_object)
    if not isinstance(config, dict) or set(config) != CONFIG_FIELDS or type(config["version"]) is not int or config["version"] != 1:
        raise ValueError("Repair queue requires the exact version 1 configuration fields")
    for key in ("runner_binary", "schedule_state_dir", "repair_config"):
        if not isinstance(config[key], str):
            raise ValueError("Repair queue paths must be strings")
        schedule.checked_path(config[key])
    for key in ("runner_sha256", "repair_config_sha256"):
        if not isinstance(config[key], str) or not re.fullmatch(r"[a-f0-9]{64}", config[key]):
            raise ValueError("Repair queue digests must be full SHA256 values")
    if schedule.runner_digest(Path(config["runner_binary"])) != config["runner_sha256"]:
        raise ValueError("Repair runner changed; review and refresh the queue configuration")
    policy = private_bytes(config["repair_config"], schedule.MAX_FILE)
    if hashlib.sha256(policy).hexdigest() != config["repair_config_sha256"]:
        raise ValueError("Repair policy changed; review and refresh the queue configuration")
    directory = Path(config["schedule_state_dir"])
    info = directory.stat()
    if not stat.S_ISDIR(info.st_mode) or info.st_mode & 0o077 or info.st_uid != os.getuid():
        raise ValueError("Schedule state must be an owned private directory")
    return config, raw


def failed_reports(directory):
    candidates = []
    with os.scandir(directory) as entries:
        for index, entry in enumerate(entries):
            if index >= MAX_ENTRIES:
                raise ValueError("Schedule inventory exceeds 256 entries; review retained queue inputs")
            if not re.fullmatch(r"run-[0-9]{6}", entry.name):
                continue
            if not entry.is_dir(follow_symlinks=False):
                raise ValueError("Schedule run directory must not be a symlink")
            if len(candidates) == MAX_REPORTS:
                raise ValueError("Schedule inventory exceeds 64 runs; select an archived queue explicitly")
            candidates.append(Path(entry.path) / "suite/report.json")
    failed = []
    for path in sorted(candidates):
        try:
            data = private_bytes(path, MAX_REPORT_BYTES)
        except FileNotFoundError:
            continue  # A running or pre-suite attempt has no terminal report yet.
        report = json.loads(data, object_pairs_hook=unique_object)
        if not isinstance(report, dict) or report.get("status") not in {"failed", "verified"}:
            raise ValueError("Suite report must declare a terminal failed or verified status")
        if report.get("status") == "failed":
            failed.append(path)
    return failed


def call_runner(config, action, report, timeout):
    args = [config["runner_binary"], "dogfood", "repairs", action,
            "--config", config["repair_config"], "--report", str(report)]
    # A private file avoids exposing failure metadata to the user journal. The
    # trusted runner bounds its own outputs; readback still enforces a ceiling.
    with tempfile.TemporaryFile() as output, tempfile.TemporaryFile() as errors:
        process = subprocess.Popen(args, stdout=output, stderr=errors, start_new_session=True)
        try:
            code = process.wait(timeout=timeout)
        except subprocess.TimeoutExpired:
            os.killpg(process.pid, signal.SIGKILL)
            process.wait(timeout=5)
            raise RuntimeError("Repair command deadline exceeded") from None
        output.seek(0)
        data = output.read((1 << 20) + 1)
    if len(data) > 1 << 20:
        raise ValueError("Repair command output exceeds its bound")
    try:
        result = json.loads(data, object_pairs_hook=unique_object)
    except (ValueError, UnicodeError):
        raise RuntimeError(f"Repair {action} failed without a valid report (exit {code})") from None
    if not isinstance(result, dict) or not isinstance(result.get("status"), str):
        raise ValueError("Repair command returned an invalid report")
    if action == "status" and code:
        raise RuntimeError(f"Repair status failed (exit {code})")
    return result, code


def queue(path, execute=False):
    config, raw = configuration(path)
    started = time.monotonic()
    reports = failed_reports(config["schedule_state_dir"])
    checked = 0
    counts = {"blocked": 0, "busy": 0, "consumed": 0}
    for report in reports:
        if time.monotonic() - started > 60:
            raise RuntimeError("Repair queue inspection exceeded 60 seconds")
        status, _ = call_runner(config, "status", report, 15)
        checked += 1
        if status["status"] != "ready":
            if status["status"] not in counts:
                raise ValueError("Repair status returned an unknown admission state")
            counts[status["status"]] += 1
            continue
        summary = {"status": "ready", "failed_reports": len(reports), "checked": checked,
                   "report_path": str(report), "execution_key": status.get("execution_key")}
        if execute:
            # Recheck reviewed bytes immediately before dispatch. The core then
            # validates its own snapshot and acquires its durable attempt lock.
            _, current = configuration(path)
            if current != raw:
                raise RuntimeError("Queue configuration changed before dispatch")
            outcome, code = call_runner(config, "run", report, 600)
            summary.update({key: outcome.get(key) for key in
                            ("status", "execution_key", "attempt_dir", "candidate_verified", "actual_model", "usage")})
            summary["runner_exit_code"] = code
        return summary
    result = "blocked" if counts["blocked"] else "busy" if counts["busy"] else "idle"
    return {"status": result, "failed_reports": len(reports), "checked": checked,
            "admission_counts": counts, "runner_exit_code": int(execute and result == "blocked"),
            "scope": "No unconsumed eligible failed case in this bounded queue"}


def render_units(name, script, config):
    schedule.unit_name(name)
    invocation = f'/usr/bin/python3 -B {schedule.quote_argument(script)} run --config {schedule.quote_argument(config)}'
    service = schedule.MARKER + f"""[Unit]
Description=Praetor bounded local dogfood patch execution

[Service]
Type=oneshot
ExecStart={invocation}
TimeoutStartSec=12min
TimeoutStopSec=15s
KillMode=control-group
UMask=0077
NoNewPrivileges=true
MemoryMax=4G
MemorySwapMax=0
TasksMax=128
CPUQuota=200%
"""
    timer = schedule.MARKER + f"""[Unit]
Description=Check for an unconsumed local dogfood repair

[Timer]
OnActiveSec=2min
OnUnitInactiveSec=15min
Unit={name}.service
AccuracySec=30s

[Install]
WantedBy=timers.target
"""
    return {name + ".service": service.encode(), name + ".timer": timer.encode()}


def install(name, config, activate):
    _, raw = configuration(config)
    queue(config)  # Inspect any retained failures without dispatching a model.
    directory = schedule.checked_path(Path.home() / ".config/systemd/user")
    backups = schedule.checked_path(Path.home() / ".local/state/praetor/dev-schedules")
    with schedule.installation_lock(backups):
        script = snapshot_scripts(backups)
        units = render_units(name, script, config)
        directory.mkdir(parents=True, exist_ok=True, mode=0o700)
        return schedule.install_locked(name, config, raw, directory, backups, units, activate)


def snapshot_scripts(backups):
    parent = Path(__file__).absolute().parent
    files = {name: schedule.read_file(parent / name) for name in ("dev_repair.py", "dev_schedule.py")}
    digest = hashlib.sha256(b"".join(name.encode() + b"\0" + data for name, data in files.items())).hexdigest()
    destination = schedule.checked_path(backups / ("repair-runner-" + digest))
    if destination.exists():
        for name, data in files.items():
            if private_bytes(destination / name, schedule.MAX_FILE) != data:
                raise ValueError("Retained repair runner snapshot changed")
    else:
        destination.mkdir(mode=0o700)
        for name, data in files.items():
            schedule.atomic_write(destination / name, data)
    return destination / "dev_repair.py"


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("action", choices=["run", "status", "install", "disable"])
    parser.add_argument("--config", type=Path)
    parser.add_argument("--name", default="praetor-dogfood-repair-local")
    parser.add_argument("--activate", action="store_true")
    args = parser.parse_args()
    name = schedule.unit_name(args.name)
    if args.action == "disable":
        result = schedule.disable(name, Path.home() / ".config/systemd/user",
                                  Path.home() / ".local/state/praetor/dev-schedules")
    else:
        if args.config is None:
            parser.error("--config is required")
        result = install(name, args.config, args.activate) if args.action == "install" else queue(args.config, args.action == "run")
    print(json.dumps(result, indent=2))
    if result.get("runner_exit_code", 0):
        sys.exit(1)


if __name__ == "__main__":
    try:
        main()
    except (OSError, ValueError, RuntimeError, subprocess.SubprocessError) as error:
        print(f"Local repair queue failed: {error}", file=sys.stderr)
        sys.exit(1)
