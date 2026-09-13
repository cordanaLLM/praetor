#!/usr/bin/env python3
"""Compatibility entry point for existing Codex hook registrations."""

import sys

from command_guard import check, main, subprocess


if __name__ == "__main__":
    sys.exit(main())
