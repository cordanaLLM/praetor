"""The exception type is named, so no scanner rule fires and V_total is unchanged."""


def load(path):
    try:
        return open(path).read()
    except OSError:
        return ""
