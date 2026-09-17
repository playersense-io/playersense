// The importer settings for the native libraries, set from code rather than from a .meta file.
//
// Unity guesses a native plugin's platform from its folder, and it guesses wrong: on a machine
// with no iOS module installed, `Runtime/Plugins/iOS/libplayersense.a` imports as **Any
// Platform** with iOS itself unticked — so a static library is offered to Windows, Android and
// Linux builds and withheld from the one platform it is for. Measured, not assumed: that is
// what this editor produced before this file existed.
//
// The usual answer is a committed .meta file. It does not work here, because the binaries are
// a release artifact: whichever machine runs install-plugins.sh generates the .meta, and it
// generates the wrong one unless that machine happens to have every platform module installed.
// A rule in code produces the same settings everywhere, including on a machine that cannot
// build for the platform it is configuring.
//
// Runs on load rather than from OnPreprocessAsset, because a package's own assets are imported
// before its editor assembly is compiled — an import hook does not see the first import, which
// is the only one that matters.

using System;
using UnityEditor;
using UnityEngine;

namespace PlayerSense.Editor
{
    internal static class PlayerSensePluginImporter
    {
        private const string Root = "com.playersense.unity/Runtime/Plugins/";

        [InitializeOnLoadMethod]
        private static void Apply()
        {
            foreach (var plugin in PluginImporter.GetAllImporters())
            {
                if (plugin == null) continue;

                int at = plugin.assetPath.IndexOf(Root, StringComparison.Ordinal);
                if (at < 0) continue;

                // e.g. "Android/arm64-v8a/libplayersense.so"
                Configure(plugin, plugin.assetPath.Substring(at + Root.Length));
            }
        }

        private static void Configure(PluginImporter plugin, string relative)
        {
            if (!Desired(relative, out var target, out bool editor, out string editorOs,
                         out string androidCpu))
            {
                Debug.LogWarning(
                    $"[PlayerSense] '{relative}' is under Runtime/Plugins but its platform is " +
                    "not recognised, so its import settings were left alone. Add it to " +
                    "PlayerSensePluginImporter.");
                return;
            }

            // Checked before it is written, because SaveAndReimport triggers a domain reload,
            // and a reload is what calls this. Settings that already match must write nothing
            // or the editor never stops reloading.
            if (!plugin.GetCompatibleWithAnyPlatform()
                && plugin.GetCompatibleWithEditor() == editor
                && plugin.GetCompatibleWithPlatform(target)
                && (editorOs == null || plugin.GetEditorData("OS") == editorOs)
                && (androidCpu == null
                    || plugin.GetPlatformData(BuildTarget.Android, "CPU") == androidCpu))
            {
                return;
            }

            plugin.SetCompatibleWithAnyPlatform(false);
            plugin.SetCompatibleWithPlatform(target, true);
            plugin.SetCompatibleWithEditor(editor);

            if (editor)
            {
                plugin.SetEditorData("OS", editorOs);
                plugin.SetEditorData("CPU", "AnyCPU");
            }
            if (androidCpu != null)
            {
                plugin.SetPlatformData(BuildTarget.Android, "CPU", androidCpu);
            }

            plugin.SaveAndReimport();
            Debug.Log($"[PlayerSense] configured {relative} for {target}" +
                      (editor ? " and the Editor" : ""));
        }

        /// <summary>What a folder under Runtime/Plugins means.</summary>
        /// <remarks>
        /// iOS is the one to read twice: <b>not</b> the Editor. The Editor cannot load a static
        /// library, and a tick there is a startup error on every domain reload. macOS is the
        /// mirror — it <b>must</b> include the Editor, or nothing works in play mode.
        /// </remarks>
        private static bool Desired(string relative, out BuildTarget target, out bool editor,
                                    out string editorOs, out string androidCpu)
        {
            target = BuildTarget.NoTarget;
            editor = false;
            editorOs = null;
            androidCpu = null;

            if (relative.StartsWith("iOS/", StringComparison.Ordinal))
            {
                target = BuildTarget.iOS;
                return true;
            }
            if (relative.StartsWith("macOS/", StringComparison.Ordinal))
            {
                target = BuildTarget.StandaloneOSX;
                editor = true;
                editorOs = "OSX";
                return true;
            }
            if (relative.StartsWith("Android/", StringComparison.Ordinal))
            {
                target = BuildTarget.Android;
                androidCpu = AndroidCpu(relative);
                return true;
            }
            if (relative.StartsWith("Windows/", StringComparison.Ordinal))
            {
                target = BuildTarget.StandaloneWindows64;
                editor = true;
                editorOs = "Windows";
                return true;
            }
            if (relative.StartsWith("Linux/", StringComparison.Ordinal))
            {
                target = BuildTarget.StandaloneLinux64;
                editor = true;
                editorOs = "Linux";
                return true;
            }
            return false;
        }

        /// <summary>The ABI, from the folder Android itself names it by.</summary>
        private static string AndroidCpu(string relative)
        {
            if (relative.Contains("arm64-v8a")) return "ARM64";
            if (relative.Contains("armeabi-v7a")) return "ARMv7";
            if (relative.Contains("x86_64")) return "X86_64";
            return "AnyCPU";
        }
    }
}
