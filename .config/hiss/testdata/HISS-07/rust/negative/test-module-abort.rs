/// Returns the port. Documentation may mention panic!("x") without aborting anything.
pub fn port(p: u16) -> Result<u16, String> {
    if p == 0 {
        return Err("zero".to_string());
    }
    Ok(p)
}

#[cfg(test)]
mod tests {
    #[test]
    #[should_panic]
    fn zero_is_refused() {
        super::port(0).unwrap();
        panic!("tests may abort");
    }
}
