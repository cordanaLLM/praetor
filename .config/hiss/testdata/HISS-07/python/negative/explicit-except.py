# A named exception is handled deliberately; sys.exit(1) in this comment calls nothing.
def load(path):
    try:
        with open(path) as handle:
            return handle.read()
    except OSError as error:
        raise RuntimeError(f"cannot read {path}") from error
