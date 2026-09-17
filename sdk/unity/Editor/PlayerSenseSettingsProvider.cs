// Edit ▸ Project Settings ▸ PlayerSense.
//
// This exists so that the answer to "how do I add analytics" is "paste your key here", with no
// integration code at all: the runtime reads this asset before the first scene loads. It is
// also the only thing that creates the asset, so the auto-start path has a home.

using System.IO;
using UnityEditor;
using UnityEngine;

namespace PlayerSense.Editor
{
    internal static class PlayerSenseSettingsProvider
    {
        private const string AssetFolder = "Assets/Resources";
        private const string AssetPath = AssetFolder + "/" + PlayerSenseSettings.ResourcePath + ".asset";

        [SettingsProvider]
        internal static SettingsProvider Create()
        {
            var provider = new SettingsProvider("Project/PlayerSense", SettingsScope.Project)
            {
                label = "PlayerSense",
                keywords = new[] { "analytics", "playersense", "telemetry", "project key" },
                guiHandler = _ => Draw(),
            };
            return provider;
        }

        private static SerializedObject s_serialized;

        private static void Draw()
        {
            var settings = Load();
            if (settings == null)
            {
                EditorGUILayout.HelpBox(
                    "PlayerSense is not configured yet. Creating the settings asset writes " +
                    AssetPath + ", which is how the SDK finds your key at runtime.",
                    MessageType.Info);

                if (GUILayout.Button("Create settings asset", GUILayout.Height(28)))
                {
                    Create(out settings);
                    s_serialized = new SerializedObject(settings);
                }
                return;
            }

            if (s_serialized == null || s_serialized.targetObject != settings)
                s_serialized = new SerializedObject(settings);

            s_serialized.Update();

            EditorGUILayout.Space();
            EditorGUILayout.LabelField("Project", EditorStyles.boldLabel);
            Field("projectKey", "Project key",
                "Your publishable key, ps_project_live_… . It identifies the project and the " +
                "environment; it is not a secret and is meant to ship inside the game.");
            Field("endpoint", "Endpoint",
                "The ingest cell for this project. Leave the default unless you were given " +
                "another one — a cell that is wrong replies 421 and the SDK corrects itself.");
            Field("environment", "Environment",
                "development, staging or production. The key is the authority; this is a hint.");

            EditorGUILayout.Space();
            EditorGUILayout.LabelField("Behaviour", EditorStyles.boldLabel);
            Field("autoInitialize", "Start automatically",
                "Start the SDK before the first scene loads, using the key above. Turn this off " +
                "only if you want to call PlayerSense.SDK.Initialize yourself — for instance " +
                "after a consent dialog you show before anything else.");
            Field("debugLogging", "Verbose logging",
                "Log every dropped event and every refusal, not only the serious ones. Off in a " +
                "shipped build.");

            s_serialized.ApplyModifiedProperties();

            var key = settings.projectKey;
            if (string.IsNullOrEmpty(key))
            {
                EditorGUILayout.Space();
                EditorGUILayout.HelpBox(
                    "Without a project key nothing is collected, and the SDK says so once at " +
                    "startup rather than failing silently.", MessageType.Warning);
            }
            else if (!key.StartsWith("ps_project_"))
            {
                // The one mistake worth catching here: a server key or a reporting key pasted
                // into a game. Both carry privileges no shipped binary should hold, and the
                // gateway would refuse them anyway — but it would refuse them on a player's
                // device, which is a much later place to find out.
                EditorGUILayout.Space();
                EditorGUILayout.HelpBox(
                    "That does not look like a project key. A project key begins with " +
                    "ps_project_ and is the only kind meant to ship inside a game — a server or " +
                    "reporting key in a build is a key you have published.",
                    MessageType.Error);
            }
        }

        private static void Field(string property, string label, string tooltip)
        {
            var serializedProperty = s_serialized.FindProperty(property);
            if (serializedProperty == null) return;
            EditorGUILayout.PropertyField(serializedProperty, new GUIContent(label, tooltip));
        }

        private static PlayerSenseSettings Load() =>
            AssetDatabase.LoadAssetAtPath<PlayerSenseSettings>(AssetPath);

        private static void Create(out PlayerSenseSettings settings)
        {
            // Resources, deliberately: the asset has to be readable from a built player before
            // any scene exists, and Resources is the only loader that works that early without
            // asking the game to reference something.
            if (!Directory.Exists(AssetFolder))
            {
                Directory.CreateDirectory(AssetFolder);
                AssetDatabase.Refresh();
            }

            settings = ScriptableObject.CreateInstance<PlayerSenseSettings>();
            AssetDatabase.CreateAsset(settings, AssetPath);
            AssetDatabase.SaveAssets();
        }
    }
}
