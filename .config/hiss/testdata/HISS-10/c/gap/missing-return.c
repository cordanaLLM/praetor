/* gcc -Wall warns -Wreturn-type: control reaches the end of a non-void function. */
int classify(int n) {
    if (n > 0) {
        return 1;
    }
}
