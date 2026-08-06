#[cfg(target_os = "linux")]
#[test]
fn variant_str_iter_survives_release_optimization() {
    use webkit2gtk::glib::{prelude::*, Variant};

    let variant = Variant::array_from_iter::<String>([
        "foo".to_string().to_variant(),
        "bar".to_string().to_variant(),
    ]);
    let values: Vec<_> = variant.array_iter_str().unwrap().collect();

    assert_eq!(values, ["foo", "bar"]);
}
