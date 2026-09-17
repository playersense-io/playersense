//! The committed C header must match the Rust types.
//!
//! The header is what a binding author reads and what a C compiler enforces, so it cannot be
//! allowed to drift from the structs it describes. Generating it in a test rather than in a
//! build script keeps the source tree free of build-time writes while still failing loudly
//! the moment the two disagree.

use std::path::PathBuf;

#[test]
fn the_committed_header_matches_the_rust_types() {
    let crate_dir = PathBuf::from(env!("CARGO_MANIFEST_DIR"));
    let committed_path = crate_dir.join("include/playersense.h");

    let generated = cbindgen::generate(&crate_dir).expect("cbindgen could not read the crate");
    let mut fresh = Vec::new();
    generated.write(&mut fresh);
    let fresh = String::from_utf8(fresh).expect("the header is UTF-8");

    let committed = std::fs::read_to_string(&committed_path).unwrap_or_default();

    if committed.trim() != fresh.trim() {
        // Written beside the committed file rather than over it: a test that silently fixed
        // the thing it is checking would never fail twice.
        let proposed = crate_dir.join("include/playersense.h.generated");
        std::fs::write(&proposed, &fresh).expect("write the proposed header");
        panic!(
            "include/playersense.h is out of date with the Rust types.\n\
             The generated version has been written to {}.\n\
             Review it and move it into place.",
            proposed.display()
        );
    }
}
