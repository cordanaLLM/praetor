@decorator(
    "alpha",
    "beta",
)
def combine(
    a: int,
    b: int,
) -> int:
    """Combine a and b."""
    return a + b
