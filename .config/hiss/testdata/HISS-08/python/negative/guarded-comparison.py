import os
import signal

def stop(pgid):
    if not isinstance(pgid, int) or isinstance(pgid, bool) or pgid <= 1 or pgid == os.getpgrp():
        return
    os.killpg(pgid, signal.SIGKILL)
