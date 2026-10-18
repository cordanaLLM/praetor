import os

def probe(pgid):
    os.killpg(pgid, 0)
