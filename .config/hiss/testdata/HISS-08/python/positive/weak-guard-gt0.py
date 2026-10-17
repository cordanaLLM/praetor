import os
import signal

def stop(pgid):
    if pgid > 0:
        os.killpg(pgid, signal.SIGKILL)
