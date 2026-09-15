/* gcc -Wall warns -Wunused-variable on scale. */
int total(const int *values, int n) {
    int scale = 2;
    int sum = 0;
    for (int i = 0; i < n; i++) {
        sum += values[i];
    }
    return sum;
}
