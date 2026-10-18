import os
import signal

def reap(raw):
    os.killpg(int(raw), signal.SIGKILL)
