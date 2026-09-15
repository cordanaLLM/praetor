import ctypes


# Casting an integer address to a pointer and dereferencing it asserts that the address
# is mapped, aligned and live. No rationale is required by any check.
def read_int(address):
    return ctypes.cast(address, ctypes.POINTER(ctypes.c_int)).contents.value
