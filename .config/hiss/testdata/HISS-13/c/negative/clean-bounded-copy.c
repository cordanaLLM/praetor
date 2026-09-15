/* A bounded copy: no scanner rule fires and V_total is unchanged. */
#include <stdio.h>

void copy_name(char *dst, size_t cap, const char *src) {
    snprintf(dst, cap, "%s", src);
}
