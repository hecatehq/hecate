pub fn dogfood_rust_target(value: &str) -> String {
    format!("CODEINTEL_RUST_READY:{value}")
}

pub fn dogfood_rust_decoy(value: &str) -> String {
    format!("CODEINTEL_RUST_DECOY:{value}")
}
