"""Gap: httpx calls without timeout= are not tracked by the Python I/O scanner."""

import httpx


def fetch_httpx(url):
    """Fetch url with httpx.get without an explicit deadline."""
    return httpx.get(url)
