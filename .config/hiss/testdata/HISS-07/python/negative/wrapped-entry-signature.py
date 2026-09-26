import sys


# black wraps a long def header and puts its closing `) -> int:` at column zero. The header
# continues until that line, so the body below is still the script entry point.
def main(
    argv: list[str] | None = None,
    *,
    strict: bool = False,
) -> int:
    if not argv and strict:
        sys.exit(2)
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
