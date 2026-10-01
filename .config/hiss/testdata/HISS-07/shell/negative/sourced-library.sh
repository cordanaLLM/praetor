# A library without an interpreter line runs under the options of the script that sources it.
# shellcheck shell=sh
print_line() {
  printf '%s\n' "$1"
}
