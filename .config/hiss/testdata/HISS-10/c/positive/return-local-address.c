/* gcc and clang warn by default, with no -W option: the function returns the address of a
 * local whose storage ends with the call (-Wreturn-local-addr, -Wreturn-stack-address). A
 * lane built with -Werror, CMAKE_COMPILE_WARNING_AS_ERROR or meson werror fails on it. */
int *latest(void) {
    int value = 42;
    return &value;
}
