"""Gap: requests.Session method calls without timeout= are not decided without receiver types."""

import requests


def fetch_session(url):
    """Fetch url through a requests.Session without a deadline."""
    session = requests.Session()
    return session.get(url)
