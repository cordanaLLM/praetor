#include <dlfcn.h>

// dlopen/dlsym load and bind code chosen at runtime: the C form of the dynamic code
// loading HISS-08 prohibits.
void *resolve(const char *library, const char *symbol) {
	void *handle = dlopen(library, RTLD_NOW);
	return dlsym(handle, symbol);
}
