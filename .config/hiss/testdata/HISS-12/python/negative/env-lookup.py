"""The credential is read from the environment; nothing secret is in the file."""

import os


def token() -> str:
    return os.environ["SERVICE_API_TOKEN"]
