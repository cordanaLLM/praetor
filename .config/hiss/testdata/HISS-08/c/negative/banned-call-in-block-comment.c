void f(char *b) {
	/* never call gets(b) here */
	fgets(b, 10, 0);
}
