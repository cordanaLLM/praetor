// The error is propagated, not unwrapped: no scanner rule fires and V_total is unchanged.
pub fn parse_port(text: &str) -> Result<u16, std::num::ParseIntError> {
    text.parse::<u16>()
}
