import os
import signal

def assertSafeSignalPID(t, pid):
    if pid <= 1:
        raise ValueError("unsafe pid")

def stop(t, pid):
    assertSafeSignalPID(t, pid)
    os.killpg(pid, signal.SIGKILL)
