// A user-defined macro may only wrap its input (ok! returns early on Err), so this self-call is
// real recursion. The scanner cannot see the macro's expansion and leaves every call inside a
// non-standard macro's input undecided.
pub fn parse_expr(p: &mut Parser) -> Result<Expr, Error> {
    if p.eat('(') {
        let inner = ok!(parse_expr(p));
        return Ok(Expr::Group(Box::new(inner)));
    }
    Ok(Expr::Atom(p.next()))
}
