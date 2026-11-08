"""Gap: socket.create_connection without timeout= is not tracked by the Python I/O scanner."""

import socket


def connect_host(host, port):
    """Open a TCP connection with socket.create_connection without a deadline."""
    return socket.create_connection((host, port))
