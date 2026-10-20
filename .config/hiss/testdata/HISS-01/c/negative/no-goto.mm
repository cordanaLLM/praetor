#include <stdio.h>

/* A function without a goto is clean. */
int f(int n) {
	if (n < 0) {
		return -1;
	}
	return n;
}
