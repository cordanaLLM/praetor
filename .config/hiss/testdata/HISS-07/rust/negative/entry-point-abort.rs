// fn main is the binary entry point, where the abort policy allows ending the process.
fn main() {
    if std::env::args().count() > 3 {
        std::process::exit(2);
    }
    if std::env::args().count() == 0 {
        panic!("no program name");
    }
}
