/* HISS-14: a new field was inserted in the middle of a public struct rather than
 * appended, so every offset after it moves and every already-compiled caller reads the
 * wrong member. The source still compiles and the header still declares the same
 * functions, so the diff looks additive; committed as
 * "feat(api): record the connection deadline" nothing in this repository notices. */
#include <stddef.h>

struct conn {
    const char *addr;
    unsigned long deadline_ms; /* inserted here, not appended */
    int port;
};

int conn_port(const struct conn *c)
{
    if (c == NULL) {
        return -1;
    }
    return c->port;
}
