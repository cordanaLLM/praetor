function guardPid(pid) {
  if (pid <= 1) {
    throw new Error("bad pid");
  }
}

export function killGroup(pid) {
  guardPid(pid);
  process.kill(-pid, "SIGTERM");
}
