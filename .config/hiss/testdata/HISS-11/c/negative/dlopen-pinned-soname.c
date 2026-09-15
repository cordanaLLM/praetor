/* Pinned to an exact soname version that the build records in its SBOM. */
#include <dlfcn.h>

void *load_accelerator(void)
{
    return dlopen("libaccelerator.so.3.4.1", RTLD_NOW);
}
