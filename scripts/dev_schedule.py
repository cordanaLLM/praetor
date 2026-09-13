#!/usr/bin/env python3
"""Install, inspect, or disable a local dogfood user timer without deleting evidence."""

import argparse
from contextlib import contextmanager
import fcntl
import hashlib
import json
import os
from pathlib import Path
import re
import stat
import subprocess
import sys
import tempfile

MARKER = "# Managed by praetor scripts/dev_schedule.py v1\n"
MAX_FILE = 65536
sys.dont_write_bytecode = True


def command(args, check=True):
    result = subprocess.run(args, capture_output=True, text=True, timeout=30, check=False)
    if check and result.returncode:
        raise RuntimeError(f"Command failed ({result.returncode}): {args[0]} {args[1:3]}; {result.stderr.strip()}")
    return result


def systemctl(*args, check=True):
    return command(["systemctl", "--user", *args], check=check)


def checked_path(value):
    path = Path(value)
    if not path.is_absolute() or str(path) != str(value) or ".." in path.parts:
        raise ValueError("Paths must be clean and absolute")
    if any(ord(char) < 32 or ord(char) == 127 for char in str(path)):
        raise ValueError("Control characters are not allowed in paths")
    for parent in reversed((path, *path.parents)):
        if parent.is_symlink():
            raise ValueError(f"Symlink path component refused: {parent}")
    return path


def read_file(path, missing=False):
    try:
        descriptor = os.open(path, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
    except FileNotFoundError:
        if missing:
            return None
        raise
    with os.fdopen(descriptor, "rb") as stream:
        info = os.fstat(stream.fileno())
        if not stat.S_ISREG(info.st_mode) or info.st_size > MAX_FILE:
            raise ValueError(f"Expected bounded regular file: {path}")
        data = stream.read(MAX_FILE + 1)
        if len(data) > MAX_FILE:
            raise ValueError(f"File exceeds {MAX_FILE} bytes: {path}")
        return data


def runner_digest(path):
    descriptor = os.open(path, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
    with os.fdopen(descriptor, "rb") as stream:
        before = os.fstat(stream.fileno())
        if not stat.S_ISREG(before.st_mode) or not before.st_mode & 0o111 or before.st_size > 256 << 20:
            raise ValueError("Schedule runner must be a regular executable of at most 256 MiB")
        digest = hashlib.sha256()
        for index in range(257):
            chunk = stream.read(1 << 20)
            if not chunk:
                break
            if index == 256:
                raise ValueError("Schedule runner exceeds 256 MiB")
            digest.update(chunk)
        after = os.fstat(stream.fileno())
        if (before.st_size, before.st_mtime_ns, before.st_ctime_ns) != (after.st_size, after.st_mtime_ns, after.st_ctime_ns):
            raise ValueError("Schedule runner changed during readback")
        return digest.hexdigest()


def atomic_write(path, data, mode=0o600):
    descriptor, temporary = tempfile.mkstemp(prefix=".praetor-unit-", dir=path.parent)
    try:
        with os.fdopen(descriptor, "wb") as stream:
            os.fchmod(stream.fileno(), mode)
            stream.write(data)
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(temporary, path)
    finally:
        Path(temporary).unlink(missing_ok=True)


@contextmanager
def installation_lock(directory):
    directory.mkdir(parents=True, exist_ok=True, mode=0o700)
    descriptor = os.open(directory / ".install.lock", os.O_CREAT | os.O_RDWR | os.O_NOFOLLOW | os.O_NONBLOCK, 0o600)
    try:
        if not stat.S_ISREG(os.fstat(descriptor).st_mode):
            raise ValueError("Installer lock must be a regular file")
        try:
            fcntl.flock(descriptor, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError as error:
            raise RuntimeError("Another schedule installer is running") from error
        yield
    finally:
        os.close(descriptor)


def unit_name(name):
    if not re.fullmatch(r"praetor-dogfood-[a-z0-9][a-z0-9-]{0,47}", name):
        raise ValueError("Unit name must start with praetor-dogfood- and use lowercase letters, digits, or hyphens")
    return name


def quote_argument(value, executable=False):
    checked_path(value)
    if executable and any(char in str(value) for char in ('"', "\\")):
        raise ValueError("systemd executable paths cannot contain quotes or backslashes")
    escaped = str(value).replace("\\", "\\\\").replace('"', '\\"').replace("%", "%%")
    # systemd expands variables in arguments, never in the executable token.
    if not executable:
        escaped = escaped.replace("$", "$$")
    return '"' + escaped + '"'


def render_units(name, binary, config):
    unit_name(name)
    invocation = f"{quote_argument(binary, executable=True)} dogfood schedule run --config {quote_argument(config)}"
    service = MARKER + f"""[Unit]
Description=Praetor local dogfood verification and repair triage

[Service]
Type=oneshot
ExecStart={invocation}
TimeoutStartSec=17min
TimeoutStopSec=15s
KillMode=control-group
UMask=0077
NoNewPrivileges=true
"""
    timer = MARKER + f"""[Unit]
Description=Check whether the Praetor local dogfood suite is due

[Timer]
OnActiveSec=2min
OnUnitInactiveSec=15min
Unit={name}.service
AccuracySec=30s

[Install]
WantedBy=timers.target
"""
    return {name + ".service": service.encode(), name + ".timer": timer.encode()}


def timer_state(name):
    enabled = systemctl("is-enabled", name + ".timer", check=False)
    active = systemctl("is-active", name + ".timer", check=False)
    if enabled.stdout.strip() not in {"enabled", "disabled", "not-found"}:
        raise RuntimeError(f"Unsupported timer enable state: {enabled.stdout.strip() or enabled.stderr.strip()}")
    if active.stdout.strip() not in {"active", "inactive", "failed"}:
        raise RuntimeError(f"Unsupported timer active state: {active.stdout.strip() or active.stderr.strip()}")
    return {"enabled": enabled.stdout.strip() == "enabled", "active": active.stdout.strip() == "active"}


def require_idle(name):
    result = systemctl("is-active", name + ".service", check=False)
    if result.stdout.strip() not in {"inactive", "failed"}:
        raise RuntimeError("The dogfood service may be running; retry installation after it finishes")


def require_managed_resolution(directory, units):
    for filename in units:
        fragment = systemctl("show", filename, "--property=FragmentPath", "--value").stdout.strip()
        overrides = systemctl("show", filename, "--property=DropInPaths", "--value").stdout.strip()
        if overrides or fragment and fragment != str(directory / filename):
            raise RuntimeError(f"Refusing foreign loaded unit or drop-ins: {filename}")


def restore_timer(name, previous):
    systemctl("enable" if previous["enabled"] else "disable", name + ".timer")
    systemctl("start" if previous["active"] else "stop", name + ".timer")


def rollback(name, directory, previous, modes, units, changed, timer):
    failures = []
    for verb in ("stop", "disable"):
        try:
            systemctl(verb, name + ".timer")
        except (OSError, RuntimeError, subprocess.SubprocessError) as error:
            failures.append(str(error))
    for filename in reversed(changed):
        try:
            target = directory / filename
            actual = read_file(target, missing=True)
            if actual == previous[filename]:
                if actual is not None and stat.S_IMODE(target.stat().st_mode) != modes[filename]:
                    atomic_write(target, actual, modes[filename])
                continue
            if actual != units[filename]:
                raise RuntimeError(f"Rollback refuses concurrently changed file: {target}")
            if previous[filename] is None:
                target.unlink()
            else:
                atomic_write(target, previous[filename], modes[filename])
        except (OSError, ValueError, RuntimeError) as error:
            failures.append(str(error))
    try:
        systemctl("daemon-reload")
        # A newly installed timer no longer exists after file rollback.
        if previous[name + ".timer"] is not None:
            restore_timer(name, timer)
    except (OSError, RuntimeError, subprocess.SubprocessError) as error:
        failures.append(str(error))
    return failures


def install(name, binary, config, directory, backups, activate):
    units = render_units(name, binary, config)
    directory, backups = checked_path(directory), checked_path(backups)
    config_data = read_file(checked_path(config))
    status = command([str(binary), "dogfood", "schedule", "status", "--config", str(config)])
    report = json.loads(status.stdout)
    if not isinstance(report, dict) or not isinstance(report.get("config"), dict) or report["config"].get("runner_binary") != str(binary):
        raise ValueError("Schedule runner_binary must match the installed service executable")
    if report.get("runner_sha256") != runner_digest(binary):
        raise ValueError("Schedule runner digest changed since status verification")
    with installation_lock(backups):
        directory.mkdir(parents=True, exist_ok=True, mode=0o700)
        return install_locked(name, config, config_data, directory, backups, units, activate)


def install_locked(name, config, config_data, directory, backups, units, activate):
    previous = {filename: read_file(directory / filename, missing=True) for filename in units}
    modes = {key: stat.S_IMODE((directory / key).stat().st_mode)
             if data is not None else 0o600 for key, data in previous.items()}
    for filename, data in previous.items():
        if data is not None and not data.startswith(MARKER.encode()):
            raise ValueError(f"Refusing unmanaged unit: {directory / filename}")
    require_managed_resolution(directory, units)
    timer = timer_state(name)
    require_idle(name)
    backup = Path(tempfile.mkdtemp(prefix="install-", dir=backups))
    report = {"name": name, "config": str(config), "config_sha256": hashlib.sha256(config_data).hexdigest(),
              "backup_dir": str(backup), "previous_timer": timer, "activated": activate,
              "previous_modes": modes,
              "units": {key: hashlib.sha256(value).hexdigest() for key, value in units.items()}}
    for filename, data in previous.items():
        if data is not None:
            atomic_write(backup / filename, data, modes[filename] & 0o600)
    atomic_write(backup / "manifest.json", (json.dumps(report, indent=2) + "\n").encode())
    with tempfile.TemporaryDirectory(prefix="validate-", dir=backups) as temporary:
        for filename, data in units.items():
            atomic_write(Path(temporary) / filename, data)
        command(["systemd-analyze", "--user", "verify", *[str(Path(temporary) / key) for key in units]])
    changed = []
    try:
        if timer["active"]:
            systemctl("stop", name + ".timer")
        require_idle(name)
        if read_file(config) != config_data:
            raise RuntimeError("Schedule config changed during installation")
        for filename, data in units.items():
            if read_file(directory / filename, missing=True) != previous[filename]:
                raise RuntimeError(f"Unit changed before replacement: {filename}")
            changed.append(filename)
            atomic_write(directory / filename, data, modes[filename] & 0o600)
        systemctl("daemon-reload")
        if activate:
            systemctl("enable", name + ".timer")
            systemctl("start", name + ".timer")
        else:
            restore_timer(name, timer)
        actual = timer_state(name)
        expected = {"active": True, "enabled": True} if activate else timer
        if actual != expected:
            raise RuntimeError(f"Timer readback mismatch: {actual}")
        report["timer"] = actual
        atomic_write(backup / "installed.json", (json.dumps(report, indent=2) + "\n").encode())
    except (OSError, ValueError, RuntimeError, subprocess.SubprocessError) as error:
        failures = rollback(name, directory, previous, modes, units, changed, timer)
        raise RuntimeError(f"Schedule installation failed: {error}; rollback errors: {failures}; backup: {backup}") from error
    return report


def disable(name, directory, backups):
    unit_name(name)
    directory, backups = checked_path(directory), checked_path(backups)
    filenames = [name + suffix for suffix in (".service", ".timer")]
    with installation_lock(backups):
        for filename in filenames:
            if not read_file(directory / filename).startswith(MARKER.encode()):
                raise ValueError("Refusing to disable unmanaged units")
        require_managed_resolution(directory, filenames)
        systemctl("disable", "--now", name + ".timer")
        actual = timer_state(name)
        if actual != {"enabled": False, "active": False}:
            raise RuntimeError(f"Disabled timer readback mismatch: {actual}")
        return {"name": name, "timer": actual, "retained_units_and_evidence": True}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("action", choices=["install", "status", "disable"])
    parser.add_argument("--name", default="praetor-dogfood-local")
    parser.add_argument("--config", type=Path)
    parser.add_argument("--binary", type=Path, default=Path.home() / ".local/bin/praetorctl")
    parser.add_argument("--activate", action="store_true")
    args = parser.parse_args()
    name = unit_name(args.name)
    directory = checked_path(Path.home() / ".config/systemd/user")
    if args.action == "install":
        if args.config is None:
            parser.error("install requires --config")
        report = install(name, args.binary, args.config, directory,
                         Path.home() / ".local/state/praetor/dev-schedules", args.activate)
    elif args.action == "disable":
        report = disable(name, directory, Path.home() / ".local/state/praetor/dev-schedules")
    else:
        report = {"name": name, "timer": timer_state(name),
                  "service": systemctl("show", name + ".service", "--property=ActiveState,SubState,Result,ExecMainStatus").stdout.strip()}
    print(json.dumps(report, indent=2))


if __name__ == "__main__":
    try:
        main()
    except (OSError, ValueError, RuntimeError, subprocess.SubprocessError) as error:
        print(f"Development schedule failed: {error}", file=sys.stderr)
        sys.exit(1)
