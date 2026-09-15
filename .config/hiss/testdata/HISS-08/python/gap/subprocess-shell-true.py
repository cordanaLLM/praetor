import subprocess


# shell=True interposes /bin/sh over a runtime-built string, the Python form of
# dynamic execution.
def run(command):
    return subprocess.run(command, shell=True, check=False)
