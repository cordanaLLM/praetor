// Reinterpreting a byte buffer as a wider type and dereferencing it asserts alignment
// and provenance the compiler cannot check, with no rationale recorded.
unsigned long widen(const unsigned char *buf) {
	return *(const unsigned long *)buf;
}
