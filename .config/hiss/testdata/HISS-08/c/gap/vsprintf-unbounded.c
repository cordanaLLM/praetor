#include <stdarg.h>

// vsprintf is the va_list form of sprintf and writes without a bound.
void format_name(char *dst, const char *fmt, va_list ap) {
	vsprintf(dst, fmt, ap);
}
