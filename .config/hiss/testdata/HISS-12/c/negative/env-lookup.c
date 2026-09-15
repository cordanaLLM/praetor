/* The credential is read from the environment; nothing secret is in the file. */
#include <stdlib.h>

const char *service_token(void)
{
    return getenv("SERVICE_API_TOKEN");
}
