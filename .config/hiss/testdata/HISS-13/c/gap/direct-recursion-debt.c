/* Real HISS-01 debt: the call graph is not a DAG. The Go scanner reports exactly this shape
   ("Direct recursion in ...; the call graph must form an acyclic DAG"); the C scanner reports
   only goto, so nothing enters the baseline and V_total does not move. */
unsigned long factorial(unsigned int n) {
    if (n <= 1) {
        return 1;
    }
    return n * factorial(n - 1);
}
