void copy_name(char *dst, const char *src) {
	g_strcpy(dst, src);
	my_sprintf(dst, src);
	buffer_gets(dst);
}
