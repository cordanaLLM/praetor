// Added debt the scanner counts: HISS-07 .unwrap() raises V_total.
pub fn parse_port(text: &str) -> u16 {
    text.parse::<u16>().unwrap()
}
