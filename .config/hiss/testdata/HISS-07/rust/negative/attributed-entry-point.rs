// An attribute that shares the header line still leaves fn main the binary entry point.
#[tokio::main] async fn main() {
    if std::env::args().count() == 0 {
        panic!("no program name");
    }
}
