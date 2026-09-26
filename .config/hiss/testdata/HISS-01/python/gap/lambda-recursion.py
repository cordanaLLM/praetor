# A lambda bound to a module name is not a def, so the scanner opens no function for it.
factorial = lambda n: 1 if n <= 1 else n * factorial(n - 1)  # noqa: E731
