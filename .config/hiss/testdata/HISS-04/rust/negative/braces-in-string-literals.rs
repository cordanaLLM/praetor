fn brace_strings() -> String {
    let open = "{";
    let closed = "}";
    let both = "{ nested { deeper } }";
    format!("{}{}{}", open, closed, both)
}
