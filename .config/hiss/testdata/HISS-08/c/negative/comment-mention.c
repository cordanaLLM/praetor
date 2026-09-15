void copy_name(char *dst, const char *src, unsigned long cap) {
	// strcpy(dst, src) was replaced by the bounded form below.
	strncpy(dst, src, cap);
}
