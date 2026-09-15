#include <stdio.h>

// popen() spawns a shell around a runtime-built command string.
FILE *open_pipe(const char *command) {
	return popen(command, "r");
}
