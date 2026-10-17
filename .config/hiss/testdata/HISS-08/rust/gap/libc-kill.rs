// Gap: Rust libc::kill with negated pid is not checked by the HISS-08 scanner
pub unsafe fn stop_group(pid: i32) {
    libc::kill(-pid, libc::SIGKILL);
}
