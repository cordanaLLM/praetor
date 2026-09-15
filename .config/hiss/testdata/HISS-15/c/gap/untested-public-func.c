/* A public interface with no test of any dimension. No C compiler, test runner or coverage
   tool is invoked by any gate in this repository, so nothing observes the absence. */
int clamp(int value, int low, int high) {
    if (value < low) {
        return low;
    }
    if (value > high) {
        return high;
    }
    return value;
}
