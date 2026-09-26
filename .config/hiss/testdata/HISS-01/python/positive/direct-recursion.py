def factorial(n):
    """factorial calls itself by its bare name, which HISS-01 forbids."""
    if n <= 1:
        return 1
    return n * factorial(n - 1)
