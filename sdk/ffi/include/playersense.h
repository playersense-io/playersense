/*
 * PlayerSense — the SDK protocol, implemented once.
 *
 * GENERATED from sdk/ffi by cbindgen. Do not edit by hand: run
 * `cargo test -p playersense-ffi` and move the regenerated file into place.
 *
 * ============================================================================
 * OWNERSHIP — four rules, and every function obeys one of them
 * ============================================================================
 *
 * R1  Anything passed IN is borrowed for the duration of the call. The callee
 *     copies what it needs before returning. A NULL pointer is legal only with
 *     a zero length; a negative length is PS_ERR_INVALID_ARGUMENT.
 *
 * R2  NOTHING IS EVER RETURNED OWNED. There is no ps_string_free, because no
 *     function hands you memory — so a leak is not representable. Values come
 *     back through a buffer you provide plus a required-length out-parameter;
 *     pass (NULL, 0) to ask how much you need.
 *
 * R3  Buffers flowing host -> core go through a PsSink callback. Neither side
 *     frees the other's memory, and your buffer need only live for the call.
 *
 * R4  Optional values use a natural sentinel, never a companion flag:
 *     PsStr { NULL, 0 } is absent, and a negative retry_after_ms is absent.
 *
 * Every length is int64_t, never size_t: size_t is four bytes on 32-bit
 * Android and eight elsewhere, which makes a binding's declaration
 * bitness-dependent. Every boolean is int32_t, never _Bool: C#'s bool marshals
 * as a 4-byte BOOL and the mismatch is silent.
 *
 * ============================================================================
 * RETURN VALUES — check the SIGN, never equality with PS_OK
 * ============================================================================
 *
 *     status <  0   failure. The call did nothing.
 *     status == 0   PS_OK.
 *     status >  0   it worked, and there is something to tell you.
 *
 * The positive range exists so that the SDK can distinguish outcomes without
 * breaking callers. ps_client_track returns PS_HELD_FOR_CONSENT when the player
 * has not answered the consent dialog yet and PS_DECLINED_BY_CONSENT when they
 * have declined — neither is an error, and a binding that treats them as one
 * will log a warning for every event a declining player generates.
 *
 * So: `if (status < 0) { report(); }`. A binding written as
 * `if (status != PS_OK)` is wrong today and will be wrong again later.
 *
 * ============================================================================
 * THREADING
 * ============================================================================
 *
 * SAFETY:  every ps_client_* function may be called from any thread. Calls on
 *          one handle are serialized internally.
 *
 * LATENCY: the "never blocks measurably" guarantee holds only when all calls on
 *          a handle come from one thread. ps_client_on_response compresses a
 *          batch while holding the lock, so a binding SHOULD marshal
 *          completions onto the thread that calls ps_client_track. The lock is
 *          insurance against a mistake, not a licence to be multi-threaded.
 *
 * A host callback MUST NOT call back into ps_client_* on the same handle. Doing
 * so returns PS_ERR_REENTRANT and does nothing, rather than deadlocking.
 *
 * ============================================================================
 * PANICS AND EXCEPTIONS
 * ============================================================================
 *
 * A panic inside the core is caught at this boundary, the handle is poisoned,
 * and every later call on it returns PS_ERR_POISONED. The game keeps running
 * with analytics off, which is the correct fail-safe.
 *
 * The reverse is NOT true. The host callbacks below are extern "C", so a
 * foreign exception crossing one — a C# exception, a Swift fatalError, a C++
 * throw — ABORTS THE PROCESS at that frame. This is deliberate: letting a
 * foreign payload unwind through Rust frames risks memory corruption, and a
 * deterministic abort with a usable stack is the better of two bad outcomes.
 *
 * SO: WRAP EVERY CALLBACK BODY IN A CATCH-ALL. In C#, every callback must also
 * be a STATIC method (IL2CPP requires it), and under Mono any delegate passed
 * as a function pointer must be ROOTED IN A STATIC FIELD or the collector will
 * take it and your second callback will crash.
 *
 * ============================================================================
 * BEFORE ANYTHING ELSE
 * ============================================================================
 *
 * Call ps_abi_selfcheck(). It is what catches a struct layout that drifted
 * while both sides still report the same ABI version, and it turns silent heap
 * corruption into one log line.
 */


#ifndef PLAYERSENSE_H
#define PLAYERSENSE_H

#include <stdarg.h>
#include <stdbool.h>
#include <stdint.h>
#include <stdlib.h>

/**
 * The ABI this build speaks.
 *
 * A single monotonic integer before 1.0: any change to a struct or a signature is breaking,
 * so there is nothing to negotiate. A binding compares this against its own constant and
 * refuses to continue on a mismatch, which turns a silently corrupt struct layout into one
 * log line.
 */
#define PS_ABI_VERSION 2

#define PS_OK 0

/**
 * The event is held in memory, waiting for the player to answer the consent dialog.
 *
 * It is delivered if they grant and discarded if they decline, and it never touches the disk
 * either way. **Not a failure** — a binding that warns here warns once per event on every
 * launch before the dialog is answered.
 */
#define PS_HELD_FOR_CONSENT 1

/**
 * The event was discarded because the player has declined analytics.
 *
 * **Not a failure** either: this is the SDK obeying them. It is distinguished from
 * [`PS_OK`] so that a diagnostics screen can say which of the two happened, which was
 * impossible when both returned zero.
 */
#define PS_DECLINED_BY_CONSENT 2

/**
 * The handle is unknown, or belonged to a client that has been destroyed.
 */
#define PS_ERR_BAD_HANDLE -1

/**
 * The host vtable is the wrong size or is missing a required function.
 */
#define PS_ERR_BAD_HOST -2

/**
 * A null pointer with a non-zero length, a negative length, or invalid UTF-8 where the
 * protocol requires ASCII.
 */
#define PS_ERR_INVALID_ARGUMENT -3

/**
 * The provided buffer was too small; `*out_len` holds the size required.
 */
#define PS_ERR_BUFFER_TOO_SMALL -4

/**
 * A host callback called back into this client. See [`PsHost`].
 */
#define PS_ERR_REENTRANT -5

/**
 * A panic was caught inside this client, which is now inert.
 */
#define PS_ERR_POISONED -6

/**
 * The binding and the binary disagree about the ABI.
 */
#define PS_ERR_ABI_MISMATCH -7

/**
 * The project has not published a flag with that key.
 *
 * Distinct from every other result on purpose, and distinct from a value: a binding folds it
 * into the game's own fallback, and cannot do that if "not published" and "published as
 * false" arrive as the same answer. Not an error a caller should log — most games ask about
 * flags that only some environments have.
 *
 * **3, not 1.** Every non-negative code in this ABI means something different, and 1 already
 * means [`PS_HELD_FOR_CONSENT`]. Two meanings for one number is how a binding that checks
 * `status > 0` generically ends up treating a missing flag as a held event.
 */
#define PS_NOT_FOUND 3

/**
 * The event name is empty, too long, or outside `[a-zA-Z0-9_.:-]`.
 */
#define PS_REJECTED_EVENT_NAME -20

/**
 * More properties than one event may carry.
 */
#define PS_REJECTED_TOO_MANY_PROPERTIES -21

/**
 * The context key is empty, too long, or outside `[a-zA-Z0-9_.-]`.
 */
#define PS_REJECTED_CONTEXT_KEY -40

/**
 * The context key is in a namespace PlayerSense resolves for itself. The owner is named in
 * `ps_last_error_message`.
 */
#define PS_REJECTED_CONTEXT_RESERVED -41

/**
 * That scope already holds as many keys as it may.
 */
#define PS_REJECTED_CONTEXT_FULL -42

#define PS_VALUE_STR 0

#define PS_VALUE_INT 1

#define PS_VALUE_FLOAT 2

#define PS_VALUE_BOOL 3

#define PS_SCOPE_SESSION 0

#define PS_SCOPE_PLAYER 1

#define PS_LEVEL_DEBUG 0

#define PS_LEVEL_INFO 1

#define PS_LEVEL_WARN 2

#define PS_LEVEL_ERROR 3

/**
 * A borrowed UTF-8 string. **Not** NUL-terminated: the length is authoritative.
 *
 * `{ NULL, 0 }` is the empty string, and where a value is optional it is also `None`.
 */
typedef struct PsStr {
    const uint8_t *ptr;
    int64_t len;
} PsStr;

/**
 * The configuration, mirroring `playersense_core::Config`.
 *
 * **Fields are append-only and never reordered.** Combined with `struct_size`, that is what
 * lets a binary with a twenty-fifth tunable keep working with a binding compiled against
 * twenty-four.
 *
 * Zero means "use the default" for every numeric field, which is why
 * [`crate::ps_config_default`] exists: twenty-four fields is a lot to get right, and a
 * forgotten `flush_interval_ms` would otherwise clamp to 100 ms and hammer the endpoint.
 */
typedef struct PsConfig {
    uint32_t struct_size;
    uint32_t _pad;
    struct PsStr api_key;
    struct PsStr endpoint;
    struct PsStr environment;
    struct PsStr sdk_name;
    struct PsStr sdk_version;
    struct PsStr app_version;
    struct PsStr app_build;
    struct PsStr platform;
    struct PsStr os_version;
    struct PsStr locale;
    /**
     * Where flags and LiveOps events come from (§42). `{ NULL, 0 }` turns the feature off:
     * every flag answers with its fallback and no event is ever running, which is what a
     * studio using only analytics wants and should not pay a request for.
     */
    struct PsStr config_endpoint;
    int64_t flush_interval_ms;
    /**
     * How often the configuration is asked for again. 0 takes the default.
     */
    int64_t config_refresh_interval_ms;
    int64_t max_events_per_batch;
    int64_t max_batch_bytes;
    int64_t session_timeout_ms;
    int64_t spool_max_age_ms;
    int64_t spool_max_bytes;
    int64_t spool_max_batches;
    int64_t preconsent_max_events;
    int64_t backoff_base_ms;
    int64_t backoff_cap_ms;
    /**
     * Whether reading a flag records that this player saw it (§44). 0 off, 1 on, -1 default.
     *
     * Three states rather than two, because a zeroed struct is the shape a binding that
     * forgot this field sends — and defaulting that to "off" would silently disable the
     * thing that makes an experiment measurable.
     */
    int32_t exposure_events;
    /**
     * The most exposures one session records. 0 takes the default.
     */
    int32_t max_exposures_per_session;
    int32_t cold_after_failures;
    /**
     * 0 or 1.
     */
    int32_t compression;
    /**
     * 0 or 1.
     */
    int32_t debug;
    int32_t _pad2;
} PsConfig;

/**
 * What a binding believes the layout to be, for [`crate::ps_abi_selfcheck`].
 *
 * This is the guard that actually prevents heap corruption, and it is the one people skip.
 * The ABI version catches a deliberate change; this catches the accident — a `bool`
 * marshalled as one byte instead of four, a `Pack` mismatch, a field reordered in a
 * refactor — where both sides still report the same version.
 */
typedef struct PsAbiSizes {
    uint32_t abi_version;
    uint32_t sizeof_host;
    uint32_t sizeof_config;
    uint32_t sizeof_stats;
    uint32_t sizeof_str;
    uint32_t sizeof_value;
    uint32_t sizeof_property;
    uint32_t sizeof_request;
} PsAbiSizes;

/**
 * Receives a buffer from the host.
 *
 * The host calls this with data it owns; the core copies immediately. Neither side frees the
 * other's memory, and the host's buffer need only be valid for the call — so a stack array,
 * an `NSData`, or a `byte[]` copied into a scratch buffer all work.
 */
typedef void (*PsSink)(void *ctx, const uint8_t *data, int64_t len);

/**
 * One HTTP request the host is asked to perform.
 *
 * Everything in it is **borrowed for the duration of the `send` call**. The host must copy
 * the body before returning — which it was going to do anyway when handing it to
 * `UnityWebRequest` or `URLSession`. Transferring ownership was rejected: a host that forgot
 * to free would leak a batch body per flush, which over a two-hour session is tens of
 * megabytes inside somebody's shipped game.
 */
typedef struct PsRequest {
    uint64_t id;
    struct PsStr url;
    struct PsStr api_key;
    /**
     * `{ NULL, 0 }` when the body is not compressed.
     */
    struct PsStr content_encoding;
    /**
     * Sent as `If-None-Match`, or `{ NULL, 0 }` when the request is not conditional.
     *
     * A host that ignores it is still correct: it receives a body where it could have had a
     * 304, and the configuration it gets is the same either way. Only bandwidth is lost.
     */
    struct PsStr if_none_match;
    const uint8_t *body;
    int64_t body_len;
} PsRequest;

/**
 * The platform, as function pointers.
 *
 * # Every callback must be wrapped in a catch-all
 *
 * These are `extern "C"`, so a foreign exception crossing one — a C# exception, a Swift
 * `fatalError` — **aborts the process at that frame**. That is deliberate: the alternative,
 * letting a foreign payload unwind through the state machine's frames, risks memory
 * corruption, and a deterministic abort with a usable stack is the better of two bad
 * outcomes.
 *
 * So every binding MUST catch everything inside every callback body. In C#, every callback
 * must also be a **static** method (IL2CPP requires it) and, under Mono, any delegate passed
 * as a function pointer must be **rooted in a static field** or the collector will take it
 * and the second callback will crash.
 *
 * # A callback must not call back
 *
 * The client's lock is held while these run. A host whose `send` synchronously completes the
 * request would deadlock, so re-entering on the same handle returns [`PS_ERR_REENTRANT`] and
 * does nothing. `send` starts a request and returns; the completion arrives later through
 * `ps_client_on_response`.
 */
typedef struct PsHost {
    /**
     * Set by the caller to `sizeof(PsHost)`.
     */
    uint32_t struct_size;
    uint32_t _pad;
    /**
     * Passed back to every callback. Typically a handle to the binding's own host object.
     */
    void *user_data;
    int64_t (*now_ms)(void*);
    int64_t (*monotonic_ms)(void*);
    void (*fill_random)(void*, uint8_t*, int64_t);
    /**
     * Returns 0 on success, non-zero on failure. The core's response to any failure is the
     * same — keep the batch in memory, where it is subject to eviction — so a message would
     * be a distinction nothing acts on.
     */
    int32_t (*store_put)(void*, struct PsStr, const uint8_t*, int64_t);
    /**
     * Calls `sink` **once** with the value, or **not at all** if the key is absent. Calling
     * it with a zero length means present and empty, which is a different fact.
     */
    void (*store_get)(void*, struct PsStr, PsSink, void*);
    void (*store_delete)(void*, struct PsStr);
    /**
     * Calls `sink` once per key carrying that prefix, in any order.
     */
    void (*store_keys)(void*, struct PsStr, PsSink, void*);
    void (*send)(void*, const struct PsRequest*);
    /**
     * Optional; may be NULL.
     */
    void (*log)(void*, int32_t, struct PsStr);
} PsHost;

/**
 * What the SDK has done. `struct_size` is set by the caller.
 */
typedef struct PsStats {
    uint32_t struct_size;
    uint32_t _pad;
    uint64_t events_tracked;
    uint64_t events_rejected;
    uint64_t events_without_consent;
    uint64_t events_evicted;
    uint64_t batches_spooled;
    uint64_t batches_delivered;
    uint64_t batches_discarded;
    uint64_t requests_failed;
    uint64_t events_accepted_by_server;
    uint64_t events_dropped_by_server;
} PsStats;

/**
 * One typed value, for an event property or a context entry.
 *
 * A flat struct rather than a union. Unions marshal badly and inconsistently across C#,
 * Swift and JNI, and sixteen saved bytes on a stack array is not worth a layout bug that
 * only appears on one platform.
 *
 * Only the field matching `type` is read.
 */
typedef struct PsValue {
    /**
     * One of the `PS_VALUE_*` constants.
     */
    int32_t kind;
    /**
     * `PS_VALUE_BOOL`: 0 or 1.
     */
    int32_t b;
    int64_t i;
    double f;
    struct PsStr s;
} PsValue;

/**
 * One named value in a property bag.
 *
 * Passed as a flat array — one crossing per event, and no handle to leak. A builder handle
 * reads better in C# and has a leak mode; a stack array cannot leak, which matters more in
 * something that lives inside a game for hours.
 */
typedef struct PsProperty {
    struct PsStr key;
    struct PsValue value;
} PsProperty;

#ifdef __cplusplus
extern "C" {
#endif // __cplusplus

/**
 * Returns [`PS_ABI_VERSION`], so a binding can check the binary rather than the header it
 * was compiled against.
 */
uint32_t ps_abi_version(void);

/**
 * Writes the defaults into a configuration.
 *
 * Start here rather than from a zeroed struct: twenty-four fields is a lot to populate
 * correctly, and a forgotten `flush_interval_ms` would clamp to the minimum and flush ten
 * times a second.
 *
 * # Safety
 * `out` must point at a writable `PsConfig`.
 */
void ps_config_default(struct PsConfig *out);

/**
 * Confirms the binding and the binary agree about every layout.
 *
 * **Call this before anything else.** The ABI version catches a deliberate change; this
 * catches the accident — a `bool` marshalled as one byte instead of four, a `Pack` mismatch,
 * a field reordered in a refactor — where both sides still report the same version. It is
 * the guard that actually prevents heap corruption, and the one people skip.
 *
 * # Safety
 * `sizes` must point at a readable [`PsAbiSizes`].
 */
int32_t ps_abi_selfcheck(const struct PsAbiSizes *sizes);

/**
 * Copies the message for the most recent failure on this thread.
 *
 * Valid only immediately after a non-`PS_OK` return; the next call clears it. Pass
 * `(NULL, 0)` to ask for the required length.
 *
 * # Safety
 * `out` must be null or point at `cap` writable bytes; `out_len` must be null or writable.
 */
int32_t ps_last_error_message(uint8_t *out, int64_t cap, int64_t *out_len);

/**
 * Creates a client. Performs no network operation.
 *
 * # Safety
 * `host` and `config` must point at readable, fully-initialized structs whose `struct_size`
 * is set, and whose strings satisfy the borrowing rule.
 */
int32_t ps_client_create(const struct PsHost *host,
                         const struct PsConfig *config,
                         uint64_t *out_handle);

/**
 * Retires a client. Safe on a poisoned one, and safe to call twice.
 */
int32_t ps_client_destroy(uint64_t client);

/**
 * Reports whether a panic was caught inside this client, leaving it inert.
 *
 * # Safety
 * `out` must point at a writable `int32_t`.
 */
int32_t ps_client_is_poisoned(uint64_t client, int32_t *out);

/**
 * Copies the installation identifier. Pass `(NULL, 0)` to ask for the required length.
 *
 * # Safety
 * As [`ps_last_error_message`].
 */
int32_t ps_client_install_id(uint64_t client, uint8_t *out, int64_t cap, int64_t *out_len);

/**
 * Fills in what the SDK has done.
 *
 * # Safety
 * `out` must point at a writable `PsStats` whose `struct_size` is set.
 */
int32_t ps_client_stats(uint64_t client, struct PsStats *out);

/**
 * The context revision, which rises on every change. A configuration refresh debounces on it.
 *
 * # Safety
 * `out` must point at a writable `uint64_t`.
 */
int32_t ps_client_context_revision(uint64_t client, uint64_t *out);

/**
 * Attaches an actor to every subsequent event. `{ NULL, 0 }` signs out.
 *
 * Does not flush: `player_id` travels per event, so a batch may straddle a sign-in.
 *
 * # Safety
 * `player_id` must satisfy the borrowing rule.
 */
int32_t ps_client_set_player(uint64_t client, struct PsStr player_id);

/**
 * Records that the player agreed to analytics collection.
 *
 * Three functions rather than one tagged call, because only one of the three states carries
 * data. A single entry point with four parameters that two states ignore is the shape that
 * eventually gets called with stale values.
 *
 * # Safety
 * The strings must satisfy the borrowing rule.
 */
int32_t ps_client_set_consent_granted(uint64_t client,
                                      struct PsStr policy_version,
                                      struct PsStr att_status,
                                      int32_t gdpr_applies,
                                      int64_t collected_at_ms);

/**
 * Records that the player refused. Collection stops and the buffer is discarded.
 */
int32_t ps_client_set_consent_denied(uint64_t client);

/**
 * Describes the player or the moment, in the studio's own vocabulary.
 *
 * Namespaces PlayerSense resolves for itself — `geo.`, `network.`, `account.` and the rest —
 * are refused, and `ps_last_error_message` names the owner.
 *
 * # Safety
 * `key` and `value` must satisfy the borrowing rule.
 */
int32_t ps_client_set_context(uint64_t client,
                              int32_t scope,
                              struct PsStr key,
                              const struct PsValue *value);

/**
 * Removes a context value.
 *
 * # Safety
 * `key` must satisfy the borrowing rule.
 */
int32_t ps_client_remove_context(uint64_t client, int32_t scope, struct PsStr key);

/**
 * Records an event. Never blocks and never throws.
 *
 * # Safety
 * `name` must satisfy the borrowing rule, and `props` must be null or point at `prop_count`
 * readable [`PsProperty`] values.
 */
int32_t ps_client_track(uint64_t client,
                        struct PsStr name,
                        const struct PsProperty *props,
                        int64_t prop_count);

/**
 * Records an event carrying money. Analytics about a purchase, not the purchase itself:
 * the authoritative record comes from the studio's backend verifying the receipt.
 *
 * # Safety
 * As [`ps_client_track`].
 */
int32_t ps_client_track_revenue(uint64_t client,
                                struct PsStr name,
                                int64_t revenue_micros,
                                struct PsStr currency,
                                const struct PsProperty *props,
                                int64_t prop_count);

/**
 * Seals whatever is queued and tries to send.
 */
int32_t ps_client_flush(uint64_t client);

/**
 * The app came back to the front.
 */
int32_t ps_client_foreground(uint64_t client);

/**
 * The app went away. Spools first, then attempts the network.
 */
int32_t ps_client_background(uint64_t client);

/**
 * Called regularly — a frame loop, a timer. Cheap when nothing is due.
 */
int32_t ps_client_tick(uint64_t client);

/**
 * One last bounded attempt. Whatever does not go stays for the next launch.
 */
int32_t ps_client_shutdown(uint64_t client);

/**
 * Answers a request the host was asked to perform.
 *
 * Must be called exactly once per request, **including** when it never reached a server: pass
 * `status = 0` for a timeout, a refused connection or a cancellation. A request the core
 * never hears about again is a batch that never retries.
 *
 * # Safety
 * `body` must be null or point at `body_len` readable bytes.
 */
int32_t ps_client_on_response(uint64_t client,
                              uint64_t request_id,
                              uint16_t status,
                              int64_t retry_after_ms,
                              const uint8_t *body,
                              int64_t body_len);

/**
 * Asks the server what this player gets, now.
 *
 * A no-op when a request is already outstanding or no configuration endpoint was set. The
 * core also asks on its own — on the refresh interval and when the app comes to the front —
 * so a binding only needs this when the game has just changed something it segments on.
 */
int32_t ps_client_refresh_config(uint64_t client);

/**
 * Restores the document a previous run persisted.
 *
 * Flags only, in effect: no clock anchor comes off disk, so nothing is reported active until
 * the server has been heard from this run. Called once at start-up, before the first frame
 * asks about a flag, so that a launch with no network answers from the last good document
 * rather than from every fallback.
 */
int32_t ps_client_restore_config(uint64_t client);

/**
 * The published version behind the current answers, or 0 when none has been fetched.
 *
 * # Safety
 * `out` must be a writable `uint32_t`.
 */
int32_t ps_client_config_version(uint64_t client, uint32_t *out);

/**
 * Whether the server has been heard from in this process.
 *
 * What makes a window meaningful: until this is 1, every event list is empty, because a
 * window measured against a clock nobody has checked is a window a player can open early.
 *
 * # Safety
 * `out` must be a writable `int32_t`.
 */
int32_t ps_client_config_anchored(uint64_t client, int32_t *out);

/**
 * A boolean flag. `PS_NOT_FOUND` when the project has no flag with that key.
 *
 * The value is untouched on anything but `PS_OK`, so a binding can initialise `out` with the
 * game's own fallback and read it back regardless of the result.
 *
 * **Records an exposure** (§44), at most once per flag per config version per session. That
 * is why this takes a client rather than being a pure lookup: reading a flag is the moment a
 * player is in an experiment, and a flag nobody can measure is a flag nobody can learn from.
 * A binding inspecting flags rather than deciding with them — a debug overlay — should not
 * use these.
 *
 * # Safety
 * `key` must be valid UTF-8 of its stated length; `out` must be a writable `int32_t`.
 */
int32_t ps_client_flag_bool(uint64_t client, struct PsStr key, int32_t *out);

/**
 * A numeric flag. `PS_NOT_FOUND` when the project has no number with that key.
 *
 * # Safety
 * `key` must be valid UTF-8 of its stated length; `out` must be a writable `double`.
 */
int32_t ps_client_flag_number(uint64_t client, struct PsStr key, double *out);

/**
 * A string flag, or a JSON one as the text it was published as.
 *
 * Both kinds answer here because both are text to a caller: a game reading a `json` flag
 * wants the document to parse, and giving it a second function to remember would buy nothing.
 *
 * Follows the usual out-buffer contract: `out_len` is always the length, and a buffer too
 * small returns `PS_ERR_BUFFER_TOO_SMALL` having written nothing, so a caller sizes once and
 * calls again.
 *
 * # Safety
 * `key` must be valid UTF-8; `out` must be writable for `cap` bytes; `out_len` writable.
 */
int32_t ps_client_flag_text(uint64_t client,
                            struct PsStr key,
                            uint8_t *out,
                            int64_t cap,
                            int64_t *out_len);

/**
 * The LiveOps events on right now, as a JSON document.
 *
 * One call rather than a count and an accessor per field, because a binding then parses once
 * with whatever its platform already has instead of making four calls per event across the
 * ABI. LiveOps is read when a screen opens, not per frame, so the parse is not on a hot path.
 *
 * The shape, which is stable and is what a binding's model declares:
 *
 * ```json
 * {"anchored":true,"version":7,"server_time_ms":1789560000000,"events":[
 *   {"key":"happy_hour","kind":"offer","priority":10,
 *    "starts_at_ms":…,"ends_at_ms":…,"next_starts_at_ms":…,
 *    "payload":"{\"multiplier\":2}"}]}
 * ```
 *
 * `payload` is a **string** holding the studio's own JSON, not a nested object. A binding's
 * JSON model cannot declare a type for something PlayerSense does not understand, and the
 * game parses it with whatever it already uses for its own data.
 *
 * `events` is empty until the server has been heard from this run — see
 * [`ps_client_config_anchored`].
 *
 * # Safety
 * `out` must be writable for `cap` bytes; `out_len` writable.
 */
int32_t ps_client_liveops_active(uint64_t client, uint8_t *out, int64_t cap, int64_t *out_len);

/**
 * The events starting soon, soonest first. Same shape as [`ps_client_liveops_active`].
 *
 * # Safety
 * `out` must be writable for `cap` bytes; `out_len` writable.
 */
int32_t ps_client_liveops_upcoming(uint64_t client, uint8_t *out, int64_t cap, int64_t *out_len);

#ifdef __cplusplus
}  // extern "C"
#endif  // __cplusplus

#endif  /* PLAYERSENSE_H */
