"""The local is assigned and never read; flake8 reports F841 and nothing runs flake8."""


def total(values):
    scale = 2
    return sum(values)
