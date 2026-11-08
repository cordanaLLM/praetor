from urllib.request import urlopen


def open_url(url):
    """Open url with timeout=None, an explicit absence of a deadline (reported)."""
    return urlopen(url, timeout=None)
