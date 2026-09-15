/* Append-only: the new member is added after every existing one, so all previously
 * compiled offsets are unchanged. No caller breaks. */
#include <stddef.h>

struct conn {
    const char *addr;
    int port;
    unsigned long deadline_ms; /* appended */
};

int conn_port(const struct conn *c)
{
    if (c == NULL) {
        return -1;
    }
    return c->port;
}
