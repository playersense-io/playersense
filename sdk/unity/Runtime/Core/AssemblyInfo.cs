// Who may see the internals, and why the linker must not delete this assembly.
//
// AlwaysLinkAssembly is not a precaution. Measured on an Android IL2CPP build with stripping at
// High: without it, `PlayerSense.Runtime.dll` was absent from UnityLinker's output directory
// entirely — the whole assembly removed. That is the linker behaving correctly, and it is
// catastrophic here, because the integration this package recommends is the one with no code in
// it. A studio that pastes a project key into Project Settings and never writes a `Track` call
// has nothing referencing this assembly, so there is nothing to keep it alive, and analytics
// works perfectly in the editor while collecting nothing in the shipped build.
//
// With it, the attribute roots the assembly, [RuntimeInitializeOnLoadMethod] roots `Bootstrap`,
// and everything the runtime and the native side reach survives: `Load`, `Build`, `Vtable`,
// `StorageRoot`, `ExcludeFromBackup` and all nine host callbacks are in the stripped assembly.
// `Track` and the other game-facing verbs are not, which is right — a game that calls them
// keeps them, and a game that does not has no use for them.
//
// Two things were tried first and are **not** here, so nobody re-adds them believing they help:
//
//   * `Runtime/link.xml`. A link.xml inside a package is imported and then never passed:
//     UnityLinker's command line carries exactly two --include-link-xml arguments, both
//     Unity's own.
//   * `[assembly: Preserve]`. Measured at 512 bytes of metadata and no difference to any
//     surviving member — the list above is identical with and without it.
//
// Re-measure with `./verify.sh --players`, which fails if the assembly stops surviving.

using System.Runtime.CompilerServices;
using UnityEngine.Scripting;

// The editor tooling and the test assembly are part of this package, not consumers of it.
[assembly: InternalsVisibleTo("PlayerSense.Editor")]
[assembly: InternalsVisibleTo("PlayerSense.Tests")]

[assembly: AlwaysLinkAssembly]
