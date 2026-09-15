/* HISS-14: the exported symbol conn_connect(), published in the previous release, has
 * been deleted and replaced by conn_dial(). Every binary linked against the old soname
 * fails to resolve it, so the ABI is not append-only. Only the commit message decides
 * the outcome. */
#include <stddef.h>

struct conn {
    const char *addr;
};

int conn_dial(struct conn *out, const char *addr)
{
    if (out == NULL || addr == NULL) {
        return -1;
    }
    out->addr = addr;
    return 0;
}
