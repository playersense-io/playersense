/*
 * Compiled by a real C compiler, with the expected sizes and offsets passed in as -D
 * defines computed by Rust. If the two disagree about a single field, this file does not
 * compile — which is the closest available stand-in for the check a C# [StructLayout] needs
 * and that there is no .NET on this machine to run.
 *
 * It is never linked or executed. The compiler is the assertion.
 */

#include <stddef.h>
#include <stdint.h>

#include "playersense.h"

/* ---- sizes ------------------------------------------------------------- */

_Static_assert(sizeof(PsStr) == EXPECT_SIZEOF_PSSTR, "PsStr");
_Static_assert(sizeof(PsValue) == EXPECT_SIZEOF_PSVALUE, "PsValue");
_Static_assert(sizeof(PsProperty) == EXPECT_SIZEOF_PSPROPERTY, "PsProperty");
_Static_assert(sizeof(PsStats) == EXPECT_SIZEOF_PSSTATS, "PsStats");
_Static_assert(sizeof(PsConfig) == EXPECT_SIZEOF_PSCONFIG, "PsConfig");
_Static_assert(sizeof(PsRequest) == EXPECT_SIZEOF_PSREQUEST, "PsRequest");
_Static_assert(sizeof(PsHost) == EXPECT_SIZEOF_PSHOST, "PsHost");
_Static_assert(sizeof(PsAbiSizes) == EXPECT_SIZEOF_PSABISIZES, "PsAbiSizes");

/* ---- offsets ------------------------------------------------------------
 *
 * Sizes alone would miss a reordering that happens to preserve the total, and a reordering
 * is exactly what a careless refactor produces.
 */

_Static_assert(offsetof(PsStr, ptr) == 0, "PsStr.ptr");
_Static_assert(offsetof(PsStr, len) == 8, "PsStr.len");

_Static_assert(offsetof(PsValue, kind) == EXPECT_OFFSET_PSVALUE_KIND, "PsValue.kind");
_Static_assert(offsetof(PsValue, b) == EXPECT_OFFSET_PSVALUE_B, "PsValue.b");
_Static_assert(offsetof(PsValue, i) == EXPECT_OFFSET_PSVALUE_I, "PsValue.i");
_Static_assert(offsetof(PsValue, f) == EXPECT_OFFSET_PSVALUE_F, "PsValue.f");
_Static_assert(offsetof(PsValue, s) == EXPECT_OFFSET_PSVALUE_S, "PsValue.s");

_Static_assert(offsetof(PsProperty, key) == 0, "PsProperty.key");
_Static_assert(offsetof(PsProperty, value) == EXPECT_OFFSET_PSPROPERTY_VALUE, "PsProperty.value");

/* struct_size must be first in every caller-populated struct, or the version guard cannot
 * be read before the rest of the struct is trusted. */
_Static_assert(offsetof(PsHost, struct_size) == 0, "PsHost.struct_size must be first");
_Static_assert(offsetof(PsConfig, struct_size) == 0, "PsConfig.struct_size must be first");
_Static_assert(offsetof(PsStats, struct_size) == 0, "PsStats.struct_size must be first");

_Static_assert(offsetof(PsHost, user_data) == EXPECT_OFFSET_PSHOST_USERDATA, "PsHost.user_data");
_Static_assert(offsetof(PsHost, now_ms) == EXPECT_OFFSET_PSHOST_NOWMS, "PsHost.now_ms");
_Static_assert(offsetof(PsHost, send) == EXPECT_OFFSET_PSHOST_SEND, "PsHost.send");

/* ---- the declarations are callable --------------------------------------
 *
 * A header can have correct layouts and signatures a caller cannot actually use. This
 * exercises every shape the ABI has: an out-parameter, a borrowed string, a by-pointer
 * struct, an array of structs, and a callback assignment.
 */

static int64_t fake_now(void *ud) { (void)ud; return 0; }
static void fake_sink_user(void *ctx, const uint8_t *d, int64_t n) { (void)ctx; (void)d; (void)n; }

void playersense_header_is_usable(void);

void playersense_header_is_usable(void) {
    PsConfig config;
    ps_config_default(&config);

    const char *key = "ps_project_live_x";
    config.api_key = (PsStr){ (const uint8_t *)key, (int64_t)17 };

    PsHost host;
    host.struct_size = (uint32_t)sizeof(PsHost);
    host.user_data = NULL;
    host.now_ms = fake_now;

    PsSink sink = fake_sink_user;
    (void)sink;

    uint64_t client = 0;
    (void)ps_client_create(&host, &config, &client);

    PsValue value;
    value.kind = PS_VALUE_INT;
    value.i = 42;

    PsProperty props[2];
    props[0].key = (PsStr){ (const uint8_t *)"level", 5 };
    props[0].value = value;

    (void)ps_client_track(client, (PsStr){ (const uint8_t *)"a", 1 }, props, 1);
    (void)ps_client_set_context(client, PS_SCOPE_PLAYER, (PsStr){ NULL, 0 }, &value);
    (void)ps_client_on_response(client, 1, 202, -1, NULL, 0);

    PsStats stats;
    stats.struct_size = (uint32_t)sizeof(PsStats);
    (void)ps_client_stats(client, &stats);

    int64_t needed = 0;
    (void)ps_client_install_id(client, NULL, 0, &needed);

    PsAbiSizes sizes;
    sizes.abi_version = ps_abi_version();
    sizes.sizeof_host = (uint32_t)sizeof(PsHost);
    (void)ps_abi_selfcheck(&sizes);

    (void)ps_client_destroy(client);
}
