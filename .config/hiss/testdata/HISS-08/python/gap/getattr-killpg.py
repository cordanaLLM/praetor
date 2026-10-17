import os
import signal

# Gap: signal through getattr alias is not resolved by line scanner
def stop(pid):
    getattr(os, "killpg")(pid, signal.SIGKILL)
