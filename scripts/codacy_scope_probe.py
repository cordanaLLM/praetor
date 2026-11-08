"""Planted Codacy probe outside the HISS fixture corpus: a subprocess call Bandit and Opengrep report.

Removed again before this pull request lands; it only shows that .codacy.yml leaves code outside
.config/hiss/testdata/ under analysis.
"""

import subprocess


def run_unchecked(cmd):
    """Run cmd with no deadline, the shape Codacy's Bandit B603 and Opengrep rules flag."""
    return subprocess.run(cmd, check=True)
