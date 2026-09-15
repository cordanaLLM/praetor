"""A public interface with no test of any dimension anywhere in the repository. No coverage
floor and no dimension check exists for Python, so verify-all passes unchanged."""


def clamp(value, low, high):
    if value < low:
        return low
    if value > high:
        return high
    return value
