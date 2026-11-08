import requests


def fetch(url):
    """Fetch url without a deadline (reported)."""
    return requests.get(url)
