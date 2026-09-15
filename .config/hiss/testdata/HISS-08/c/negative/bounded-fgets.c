#include <stdio.h>

void read_name(char *dst, int cap, FILE *in) {
	fgets(dst, cap, in);
}
