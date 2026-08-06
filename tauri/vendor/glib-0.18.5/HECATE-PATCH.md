# Hecate glib 0.18.5 backport

This directory is the crates.io `glib` 0.18.5 package, licensed under the
included MIT license. Its published source records upstream commit
`42b9caf98e03ded086362d9653ca58fe94dc8658` in `.cargo_vcs_info.json`.
The crates.io archive SHA-256 is
`233daaf6e83ae6a12a52055f568f9d7cf4671dabb78ff9560ab6da230ce00ee5`.

Hecate changes exactly two tokens in `src/variant_iter.rs`: the out pointer is
mutable and is passed as `&mut p`. This is byte-for-byte equivalent to upstream
gtk-rs commit `b5a4071e439bef2b5eea76c3aa25e5ae84839e34`, which fixes
RUSTSEC-2024-0429. The old immutable reference allowed an optimized release
build to discard GLib's out-parameter write and then dereference a null pointer.

`tauri/scripts/check-glib-backport.ts` reconstructs the published iterator,
verifies its SHA-256, verifies the fixed file against the immutable upstream
commit, and hashes all 121 upstream files. Its canonical tree hash is SHA-256
over sorted `path`, NUL, byte length, NUL, raw bytes, NUL entries, excluding
only `.gitignore` and this Hecate-owned record. The reconstructed published tree
is `c52a2c6c879157ecc49c3e6aba04f4b01a1b7026823c54897711596deff71b81`;
the patched tree is
`1b51798bfba7da92001feb436b019110061f82c789c4cc6a7778fb9fd4cb9fb2`.

Tauri 2's Linux GTK3 and WebKit dependencies require `glib ^0.18`, while the
registry advisory identifies only 0.20 and newer as fixed. The gtk-rs 0.18
line is end-of-life, so Hecate owns this temporary backport instead of hiding
the advisory or depending on a mutable third-party branch.

Remove the patch when Hecate moves to a Tauri Linux stack that resolves a
maintained, fixed glib release. Until then, verify the equivalent iterator
regression through Hecate's locked application graph in an optimized Linux
build:

```sh
cargo test --manifest-path tauri/src-tauri/Cargo.toml --locked \
  --release --test glib_backport
```
