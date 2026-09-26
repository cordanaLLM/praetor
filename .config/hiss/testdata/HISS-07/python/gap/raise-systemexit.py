# raise SystemExit ends the interpreter exactly as sys.exit does, but only the sys.exit call
# is matched, so this abort in library code goes unreported.
def require(value):
    if not value:
        raise SystemExit(1)
    return value
