// A typed interpreter whose method is named eval; no dynamic execution happens.
interface Expr {
  value: number;
}

export class Calculator {
  eval(expr: Expr): number {
    return expr.value;
  }
}
