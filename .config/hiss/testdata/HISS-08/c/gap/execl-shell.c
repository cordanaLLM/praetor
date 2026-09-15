#include <unistd.h>

// execl replaces the process image with an interpreter over a runtime string.
void replace_image(const char *script) {
	execl("/bin/sh", "sh", "-c", script, (char *)0);
}
