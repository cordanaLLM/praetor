import os
import signal

def stop(pid):
    os.kill(-pid, signal.SIGKILL)
