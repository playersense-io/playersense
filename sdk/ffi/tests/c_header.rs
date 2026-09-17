//! The header compiled by a real C compiler.
//!
//! `tests/abi.rs` proves the functions behave; this proves the *header* is valid C and that a
//! C compiler lays the structs out exactly as Rust does. Those are different claims, and the
//! second is the one a binding depends on — a C# `[StructLayout]` that disagrees corrupts the
//! heap silently, and there is no .NET on this machine to check it directly.
//!
//! Rust computes the expected sizes and offsets and passes them in as `-D` defines; the C
//! file asserts its own view matches. A disagreement about one field fails the compile.

use std::mem::offset_of;
use std::path::PathBuf;
use std::process::Command;

use playersense::*;

#[test]
fn a_c_compiler_agrees_about_every_layout() {
    let crate_dir = PathBuf::from(env!("CARGO_MANIFEST_DIR"));
    let source = crate_dir.join("tests/c/layout.c");
    let include = crate_dir.join("include");

    let compiler = std::env::var("CC").unwrap_or_else(|_| "cc".to_owned());
    if Command::new(&compiler).arg("--version").output().is_err() {
        // A machine with no C compiler can still run everything else. Saying so beats a
        // failure that looks like the header is wrong.
        eprintln!("no C compiler ({compiler}); skipping the layout check");
        return;
    }

    let mut cmd = Command::new(&compiler);
    cmd.arg("-std=c11")
        .arg("-Wall")
        .arg("-Wextra")
        .arg("-Werror")
        // Syntax and semantics only: this is about the header, not about linking.
        .arg("-fsyntax-only")
        .arg("-I")
        .arg(&include)
        .arg(&source);

    for (name, value) in expectations() {
        cmd.arg(format!("-D{name}={value}"));
    }

    let output = cmd.output().expect("run the C compiler");
    assert!(
        output.status.success(),
        "the header does not compile, or C disagrees with Rust about a layout:\n{}",
        String::from_utf8_lossy(&output.stderr)
    );
}

fn expectations() -> Vec<(&'static str, usize)> {
    vec![
        ("EXPECT_SIZEOF_PSSTR", size_of::<PsStr>()),
        ("EXPECT_SIZEOF_PSVALUE", size_of::<PsValue>()),
        ("EXPECT_SIZEOF_PSPROPERTY", size_of::<PsProperty>()),
        ("EXPECT_SIZEOF_PSSTATS", size_of::<PsStats>()),
        ("EXPECT_SIZEOF_PSCONFIG", size_of::<PsConfig>()),
        ("EXPECT_SIZEOF_PSREQUEST", size_of::<PsRequest>()),
        ("EXPECT_SIZEOF_PSHOST", size_of::<PsHost>()),
        ("EXPECT_SIZEOF_PSABISIZES", size_of::<PsAbiSizes>()),
        ("EXPECT_OFFSET_PSVALUE_KIND", offset_of!(PsValue, kind)),
        ("EXPECT_OFFSET_PSVALUE_B", offset_of!(PsValue, b)),
        ("EXPECT_OFFSET_PSVALUE_I", offset_of!(PsValue, i)),
        ("EXPECT_OFFSET_PSVALUE_F", offset_of!(PsValue, f)),
        ("EXPECT_OFFSET_PSVALUE_S", offset_of!(PsValue, s)),
        (
            "EXPECT_OFFSET_PSPROPERTY_VALUE",
            offset_of!(PsProperty, value),
        ),
        (
            "EXPECT_OFFSET_PSHOST_USERDATA",
            offset_of!(PsHost, user_data),
        ),
        ("EXPECT_OFFSET_PSHOST_NOWMS", offset_of!(PsHost, now_ms)),
        ("EXPECT_OFFSET_PSHOST_SEND", offset_of!(PsHost, send)),
    ]
}
