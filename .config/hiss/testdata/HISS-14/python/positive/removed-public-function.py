"""HISS-14: the published function connect(addr) has been deleted and replaced by
dial(addr). Every importer of connect raises ImportError, so the public contract is
not append-only. Only the commit message decides the outcome."""


class Conn:
    def __init__(self, addr: str) -> None:
        self.addr = addr


def dial(addr: str) -> Conn:
    return Conn(addr)
