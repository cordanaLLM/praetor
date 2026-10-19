int f(int n) {
	if (n < 0) goto done;
	n--;
done:
	return n;
}

int g(int n) {
again:
	n--;
	if (n > 0) goto again;
	return n;
}
