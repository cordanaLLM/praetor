// A macro may rewrite its input: syscall! expands recv(..) into libc::recv, and tracing's
// debug! never calls a function named debug. A call inside such a macro's input is not
// decided.
pub fn recv(fd: i32, buf: &mut [u8]) -> i32 {
    syscall!(recv(fd, buf.as_mut_ptr(), buf.len()))
}

pub fn send(fd: i32, buf: &[u8]) -> i32 {
    let sent = syscall!(
        send(
            fd,
            buf.as_ptr(),
            buf.len(),
        )
    );
    sent
}

pub fn debug(value: &Value) {
    tracing::debug!(field = debug(&value));
}
