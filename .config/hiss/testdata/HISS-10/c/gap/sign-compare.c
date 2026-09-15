/* gcc -Wextra warns -Wsign-compare: the signed index is compared to an unsigned length. */
int fits(int index, unsigned int length) {
    return index < length;
}
