use base64::Engine as _;
use minisign_verify::{PublicKey, Signature};
use serde_json::Value;
use std::{env, error::Error, fs, path::Path};

fn decode_box(encoded: &str, label: &str) -> Result<String, Box<dyn Error>> {
    let bytes = base64::engine::general_purpose::STANDARD
        .decode(encoded.trim())
        .map_err(|error| format!("could not decode {label}: {error}"))?;
    String::from_utf8(bytes)
        .map_err(|error| format!("{label} does not contain UTF-8 text: {error}").into())
}

fn main() -> Result<(), Box<dyn Error>> {
    let arguments = env::args().skip(1).collect::<Vec<_>>();
    if arguments.len() < 3 || arguments.len() % 2 == 0 {
        return Err(
            "usage: verify_updater_signatures <tauri.conf.json> <payload> <signature> [...]".into(),
        );
    }

    let config_path = Path::new(&arguments[0]);
    let config: Value = serde_json::from_slice(&fs::read(config_path)?)?;
    let encoded_public_key = config
        .pointer("/plugins/updater/pubkey")
        .and_then(Value::as_str)
        .ok_or("tauri.conf.json is missing plugins.updater.pubkey")?;
    let public_key = PublicKey::decode(&decode_box(encoded_public_key, "updater public key")?)?;

    for pair in arguments[1..].chunks_exact(2) {
        let payload_path = Path::new(&pair[0]);
        let signature_path = Path::new(&pair[1]);
        let encoded_signature = fs::read_to_string(signature_path)?;
        let signature = Signature::decode(&decode_box(
            &encoded_signature,
            &format!("updater signature {}", signature_path.display()),
        )?)?;
        let payload = fs::read(payload_path)?;
        public_key
            .verify(&payload, &signature, true)
            .map_err(|error| {
                format!(
                    "{} does not verify against {}: {error}",
                    signature_path.display(),
                    payload_path.display()
                )
            })?;
        println!("verified updater signature: {}", payload_path.display());
    }

    Ok(())
}
