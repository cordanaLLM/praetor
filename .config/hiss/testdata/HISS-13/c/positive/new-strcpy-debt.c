/* Added debt the scanner counts: HISS-08 strcpy raises V_total. */
#include <string.h>

void copy_name(char *dst, const char *src) {
    strcpy(dst, src);
}
