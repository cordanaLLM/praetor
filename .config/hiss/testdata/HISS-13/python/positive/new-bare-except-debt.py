"""Added debt the scanner counts: HISS-07 bare except raises V_total."""


def load(path):
    try:
        return open(path).read()
    except:
        return ""
