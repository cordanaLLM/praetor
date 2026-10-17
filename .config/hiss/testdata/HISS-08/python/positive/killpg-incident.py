import os
import signal

def kill_process_group(process):
    os.killpg(os.getpgid(process.pid), signal.SIGKILL)
