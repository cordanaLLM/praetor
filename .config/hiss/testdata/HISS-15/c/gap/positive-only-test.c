/* Only the positive dimension is asserted. The negative dimension (an inverted range) and
   the boundary dimension (INT_MIN, INT_MAX, value exactly at low and at high) are absent. */
#include <assert.h>

extern int clamp(int value, int low, int high);

int main(void) {
    assert(clamp(5, 0, 10) == 5);
    return 0;
}
