#include <signal.h>

/* Gap: C kill with negated pid is not checked by HISS-08 */
int stop_group(int pid) {
    return kill(-pid, SIGKILL);
}
