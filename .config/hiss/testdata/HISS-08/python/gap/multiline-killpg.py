import os
import signal

def stop(pgid):
    os.killpg(
        pgid,
        signal.SIGKILL,
    )
