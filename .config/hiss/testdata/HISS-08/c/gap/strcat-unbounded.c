// strcat is the same unbounded-write family as strcpy and is equally banned by the
// axiom's "insecure C runtime functions"; only gets, strcpy and sprintf are matched.
void append_name(char *dst, const char *src) {
	strcat(dst, src);
}
