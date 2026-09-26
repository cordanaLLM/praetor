import sys


def run(argv):
    return 0 if argv else 2


# def main and the __main__ guard are the script entry point, where exiting is allowed.
def main():
    if len(sys.argv) > 3:
        sys.exit(2)
    return run(sys.argv[1:])


if __name__ == "__main__":
    sys.exit(main())
