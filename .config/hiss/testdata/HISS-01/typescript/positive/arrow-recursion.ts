// An arrow function bound to a const reaches itself by the binding's name.
export const factorial = (n: number): number => {
  return n <= 1 ? 1 : n * factorial(n - 1);
};
