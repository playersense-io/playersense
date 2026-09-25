//! Turning C values into Rust ones, with the validation policy in one place.
//!
//! The policy is not uniform, and the asymmetry is deliberate. A malformed **name** — an
//! event name, a context key, a currency — is refused, because those are ASCII-restricted by
//! the protocol and invalid UTF-8 cannot be a valid one. A malformed **property value** is
//! repaired and logged, because losing a whole event to one mangled string is the worse
//! outcome under §18, and Mono can hand over a lone surrogate from a C# string nobody
//! inspected.

use std::collections::BTreeMap;
use std::slice;

use playersense_core::Value;
use playersense_core::config::Config;

use crate::types::*;

/// Reads a borrowed string, refusing anything that is not valid UTF-8.
///
/// # Safety
/// `s.ptr` must be null or point at `s.len` readable bytes for the duration of the call.
pub unsafe fn str_strict<'a>(s: PsStr) -> Result<&'a str, i32> {
    let bytes = unsafe { bytes(s)? };
    std::str::from_utf8(bytes).map_err(|_| PS_ERR_INVALID_ARGUMENT)
}

/// Reads a borrowed string, repairing invalid UTF-8 rather than refusing it.
///
/// # Safety
/// As [`str_strict`].
pub unsafe fn str_lossy(s: PsStr) -> Result<(String, bool), i32> {
    let bytes = unsafe { bytes(s)? };
    match std::str::from_utf8(bytes) {
        Ok(text) => Ok((text.to_owned(), false)),
        Err(_) => Ok((String::from_utf8_lossy(bytes).into_owned(), true)),
    }
}

/// # Safety
/// As [`str_strict`].
unsafe fn bytes<'a>(s: PsStr) -> Result<&'a [u8], i32> {
    if s.len < 0 {
        return Err(PS_ERR_INVALID_ARGUMENT);
    }
    if s.ptr.is_null() {
        // A null pointer is only legal when there is nothing to read. The pair
        // `{ NULL, 5 }` is a caller bug, and dereferencing it would be this library's.
        return if s.len == 0 {
            Ok(&[])
        } else {
            Err(PS_ERR_INVALID_ARGUMENT)
        };
    }
    Ok(unsafe { slice::from_raw_parts(s.ptr, s.len as usize) })
}

/// Whether a `PsStr` means `None` rather than the empty string (R4).
pub fn is_absent(s: PsStr) -> bool {
    s.ptr.is_null() && s.len == 0
}

/// Reads one typed value.
///
/// # Safety
/// The string variant must satisfy [`str_strict`]'s contract.
pub unsafe fn value(v: &PsValue) -> Result<(Value, bool), i32> {
    Ok(match v.kind {
        PS_VALUE_STR => {
            let (text, repaired) = unsafe { str_lossy(v.s)? };
            (Value::Str(text), repaired)
        }
        PS_VALUE_INT => (Value::Int(v.i), false),
        PS_VALUE_FLOAT => (Value::Float(v.f), false),
        PS_VALUE_BOOL => (Value::Bool(v.b != 0), false),
        _ => return Err(PS_ERR_INVALID_ARGUMENT),
    })
}

/// Reads a property bag.
///
/// Returns the keys whose values had to be repaired, so the caller can say which ones in a
/// single log line rather than one per property.
///
/// # Safety
/// `props` must be null or point at `count` readable [`PsProperty`] values.
pub unsafe fn properties(
    props: *const PsProperty,
    count: i64,
) -> Result<(BTreeMap<String, Value>, Vec<String>), i32> {
    if count < 0 {
        return Err(PS_ERR_INVALID_ARGUMENT);
    }
    if count == 0 || props.is_null() {
        return Ok((BTreeMap::new(), Vec::new()));
    }

    let items = unsafe { slice::from_raw_parts(props, count as usize) };
    let mut out = BTreeMap::new();
    let mut repaired = Vec::new();

    for item in items {
        // A key is a name, so it is refused rather than repaired.
        let key = unsafe { str_strict(item.key)? };
        let (value, was_repaired) = unsafe { value(&item.value)? };
        if was_repaired {
            repaired.push(key.to_owned());
        }
        out.insert(key.to_owned(), value);
    }
    Ok((out, repaired))
}

/// Reads the configuration. A `PsConfig` smaller than this build's is refused rather than read
/// past its end: the binding and the library must agree on the struct (the ABI self-check).
///
/// Zero means "leave the default" for every numeric field. Twenty-four fields is a lot to
/// populate correctly, and a forgotten `flush_interval_ms` would otherwise clamp to the
/// minimum and flush ten times a second.
///
/// # Safety
/// `raw` must point at a `PsConfig` whose `struct_size` describes how much of it is
/// initialized, and whose strings satisfy [`str_strict`]'s contract.
pub unsafe fn config(raw: &PsConfig) -> Result<Config, i32> {
    if (raw.struct_size as usize) < size_of::<PsConfig>() {
        // Smaller than this build expects: the binding was compiled against an older
        // header, and reading the tail would read uninitialized memory.
        return Err(PS_ERR_ABI_MISMATCH);
    }

    let api_key = unsafe { str_strict(raw.api_key)? };
    let endpoint = unsafe { str_strict(raw.endpoint)? };
    let mut config = Config::new(api_key, endpoint);

    unsafe {
        set_if_given(&mut config.environment, raw.environment)?;
        set_if_given(&mut config.sdk_name, raw.sdk_name)?;
        set_if_given(&mut config.sdk_version, raw.sdk_version)?;
        set_if_given(&mut config.app_version, raw.app_version)?;
        set_if_given(&mut config.app_build, raw.app_build)?;
        set_if_given(&mut config.platform, raw.platform)?;
        set_if_given(&mut config.os_version, raw.os_version)?;
        set_if_given(&mut config.locale, raw.locale)?;
    }

    // Absent rather than empty: a host that leaves it { NULL, 0 } is a host whose studio
    // does not use flags, and asking every quarter of an hour for an answer nobody reads is a
    // request a player pays for.
    if !is_absent(raw.config_endpoint) {
        config.config_endpoint = unsafe { str_strict(raw.config_endpoint)? }.to_owned();
    }
    if raw.config_refresh_interval_ms > 0 {
        config.config_refresh_interval_ms = raw.config_refresh_interval_ms;
    }

    if raw.exposure_events >= 0 {
        config.exposure_events = raw.exposure_events != 0;
    }
    if raw.max_exposures_per_session > 0 {
        config.max_exposures_per_session = raw.max_exposures_per_session as usize;
    }

    if raw.flush_interval_ms > 0 {
        config.flush_interval_ms = raw.flush_interval_ms;
    }
    if raw.max_events_per_batch > 0 {
        config.max_events_per_batch = raw.max_events_per_batch as usize;
    }
    if raw.max_batch_bytes > 0 {
        config.max_batch_bytes = raw.max_batch_bytes as usize;
    }
    if raw.session_timeout_ms > 0 {
        config.session_timeout_ms = raw.session_timeout_ms;
    }
    if raw.spool_max_age_ms > 0 {
        config.spool_max_age_ms = raw.spool_max_age_ms;
    }
    if raw.spool_max_bytes > 0 {
        config.spool_max_bytes = raw.spool_max_bytes as usize;
    }
    if raw.spool_max_batches > 0 {
        config.spool_max_batches = raw.spool_max_batches as usize;
    }
    if raw.preconsent_max_events > 0 {
        config.preconsent_max_events = raw.preconsent_max_events as usize;
    }
    if raw.backoff_base_ms > 0 {
        config.backoff_base_ms = raw.backoff_base_ms;
    }
    if raw.backoff_cap_ms > 0 {
        config.backoff_cap_ms = raw.backoff_cap_ms;
    }
    if raw.cold_after_failures > 0 {
        config.cold_after_failures = raw.cold_after_failures as u32;
    }

    // Booleans have no "unset", so they are read as given. `ps_config_default` writes the
    // defaults into them, which is why a caller should start from it.
    config.compression = raw.compression != 0;
    config.debug = raw.debug != 0;

    // Logs, exceptions and crashes (§15).
    unsafe { set_if_given(&mut config.device_model, raw.device_model)? };
    config.logs_enabled = raw.logs_enabled == 1;
    if let Some(level) = playersense_core::LogLevel::from_i32(raw.log_min_level) {
        config.log_min_level = level;
    }
    if let Some(level) = playersense_core::LogLevel::from_i32(raw.log_breadcrumb_level) {
        config.log_breadcrumb_level = level;
    }
    if raw.log_max_per_minute > 0 {
        config.log_max_per_minute = raw.log_max_per_minute as u32;
    }
    if raw.log_flush_interval_ms > 0 {
        config.log_flush_interval_ms = raw.log_flush_interval_ms;
    }
    config.log_scrub = raw.log_scrub != 0;
    config.log_abnormal_exits = raw.log_abnormal_exits != 0;
    config.log_hold_crashes = raw.log_hold_crashes != 0;
    config.child_directed = raw.child_directed != 0;

    Ok(config)
}

/// # Safety
/// As [`str_strict`].
unsafe fn set_if_given(field: &mut String, s: PsStr) -> Result<(), i32> {
    if is_absent(s) || s.len == 0 {
        return Ok(());
    }
    *field = unsafe { str_strict(s)?.to_owned() };
    Ok(())
}

/// Borrows a `&str` as a `PsStr` for the duration of a call.
pub fn out_str(s: &str) -> PsStr {
    PsStr {
        ptr: s.as_ptr(),
        len: s.len() as i64,
    }
}

/// Copies a value into a caller-provided buffer (R2).
///
/// Always writes the required length, so `(NULL, 0)` is a legal probe. The caller frees
/// nothing, because nothing was allocated for them.
///
/// # Safety
/// `out` must be null or point at `cap` writable bytes; `out_len` must be null or writable.
pub unsafe fn fill(value: &[u8], out: *mut u8, cap: i64, out_len: *mut i64) -> i32 {
    if !out_len.is_null() {
        unsafe { *out_len = value.len() as i64 };
    }
    if cap < 0 {
        return PS_ERR_INVALID_ARGUMENT;
    }
    if out.is_null() || (cap as usize) < value.len() {
        return PS_ERR_BUFFER_TOO_SMALL;
    }
    unsafe { std::ptr::copy_nonoverlapping(value.as_ptr(), out, value.len()) };
    PS_OK
}
