#include <stdio.h>

// scanf("%s") writes an unbounded token into a fixed buffer, the same defect that
// makes gets() banned.
void read_name(char *dst) {
	scanf("%s", dst);
}
