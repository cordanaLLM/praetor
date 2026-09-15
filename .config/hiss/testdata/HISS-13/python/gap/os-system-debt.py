"""Real HISS-08 debt: os.system hands a runtime-built string to a shell, which the rule's own
formal specification calls dynamic runtime code evaluation. Only eval and exec are matched,
so nothing enters the baseline and V_total does not move."""

import os


def deploy(target):
    return os.system("deploy " + target)
