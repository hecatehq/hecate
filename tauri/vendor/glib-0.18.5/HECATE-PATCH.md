# Hecate glib 0.18.5 backport

This directory is the crates.io `glib` 0.18.5 package, licensed under the
included MIT license. Its published source records upstream commit
`42b9caf98e03ded086362d9653ca58fe94dc8658` in `.cargo_vcs_info.json`.

Hecate changes exactly two tokens in `src/variant_iter.rs`: the out pointer is
mutable and is passed as `&mut p`. This is byte-for-byte equivalent to upstream
gtk-rs commit `b5a4071`, which fixes RUSTSEC-2024-0429. The old immutable
reference allowed an optimized release build to discard GLib's out-parameter
write and then dereference a null pointer.

Tauri 2's Linux GTK3 and WebKit dependencies require `glib ^0.18`, while the
registry advisory identifies only 0.20 and newer as fixed. The gtk-rs 0.18
line is end-of-life, so Hecate owns this temporary backport instead of hiding
the advisory or depending on a mutable third-party branch.

Remove the patch when Hecate moves to a Tauri Linux stack that resolves a
maintained, fixed glib release. Until then, verify the existing upstream
regression in an optimized Linux build:

```sh
cargo test --manifest-path tauri/vendor/glib-0.18.5/Cargo.toml \
  --release --lib variant_iter
```
