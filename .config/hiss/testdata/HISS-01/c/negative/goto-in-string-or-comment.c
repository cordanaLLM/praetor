#include <stdio.h>

/* A goto inside a string or a comment is no jump. */
int f(int goto_count) {
	puts("if (n) goto done;"); /* if (n) goto done; */
	return goto_count; // else goto done;
}
