/* HISS-11: a shared object is resolved from the runtime loader path with no pinned
 * version and no signature check, so the code that gets mapped into the process is
 * whatever happens to be installed. */
#include <dlfcn.h>

void *load_accelerator(void)
{
    return dlopen("libaccelerator.so", RTLD_NOW);
}
