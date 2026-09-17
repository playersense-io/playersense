// Window ▸ Analysis ▸ PlayerSense.
//
// The question this answers is the one every integration asks on its first afternoon: "I called
// Track — did anything happen?" Without it the honest answer is "look at the server tomorrow",
// because a correct SDK is silent by design: it never blocks, never throws and never returns a
// result. So the counters are the only evidence, and this puts them on screen.
//
// Read-only except for the test event. It shows what the SDK did; it does not configure it.

using UnityEditor;
using UnityEngine;

namespace PlayerSense.Editor
{
    internal sealed class PlayerSenseWindow : EditorWindow
    {
        private Vector2 _scroll;

        [MenuItem("Window/Analysis/PlayerSense")]
        private static void Open()
        {
            var window = GetWindow<PlayerSenseWindow>();
            window.titleContent = new GUIContent("PlayerSense");
            window.minSize = new Vector2(320, 260);
            window.Show();
        }

        // The counters move while the game runs, and a panel that only updates when the mouse
        // crosses it reads as broken.
        private void OnInspectorUpdate()
        {
            if (Application.isPlaying) Repaint();
        }

        private void OnGUI()
        {
            _scroll = EditorGUILayout.BeginScrollView(_scroll);

            if (!Application.isPlaying)
            {
                EditorGUILayout.HelpBox(
                    "Enter play mode to see what the SDK is doing. Nothing runs in edit mode.",
                    MessageType.Info);
                EditorGUILayout.EndScrollView();
                return;
            }

            if (!SDK.IsInitialized)
            {
                EditorGUILayout.HelpBox(
                    "The SDK is not running. Either there is no project key in Project Settings, " +
                    "or automatic start is off and nothing called PlayerSense.SDK.Initialize. " +
                    "The Console carries the reason.",
                    MessageType.Warning);
                EditorGUILayout.EndScrollView();
                return;
            }

            EditorGUILayout.LabelField("Installation", EditorStyles.boldLabel);
            Row("Install id", Identity.InstallId ?? "—");
            Row("Context revision", Context.Revision.ToString());

            EditorGUILayout.Space();
            EditorGUILayout.LabelField("Events", EditorStyles.boldLabel);
            var stats = SDK.Stats;
            Row("Tracked", stats.EventsTracked.ToString());
            Row("Declined by the player", stats.EventsWithoutConsent.ToString(),
                "Discarded because the player declined analytics. Not a fault — this is the " +
                "SDK obeying them. Events collected before they answer are held in memory " +
                "instead, and are counted under Tracked.");
            Warn("Refused locally", stats.EventsRejected,
                "A bad event name, or too many properties. Non-zero is an integration bug — the " +
                "Console names the event.");
            Warn("Dropped by the server", stats.EventsDroppedByServer,
                "The gateway accepted the batch and refused these rows. Non-zero is an " +
                "integration bug worth chasing: those events do not exist in any chart.");
            Warn("Evicted", stats.EventsEvicted,
                "Lost to a full or ageing spool — the device was offline long enough that the " +
                "oldest events were dropped to keep the newest.");

            EditorGUILayout.Space();
            EditorGUILayout.LabelField("Delivery", EditorStyles.boldLabel);
            Row("Batches spooled", stats.BatchesSpooled.ToString());
            Row("Delivered", stats.BatchesDelivered.ToString());
            Row("Accepted rows", stats.EventsAcceptedByServer.ToString());
            Row("Requests failed", stats.RequestsFailed.ToString(),
                "Transient by definition: a failed request is retried with backoff. A number " +
                "that climbs while Delivered does not means the endpoint or the key is wrong.");
            Warn("Batches discarded", stats.BatchesDiscarded,
                "Given up on permanently — a 400 the server will never accept, or a spool file " +
                "that could not be read.");

            EditorGUILayout.Space();
            using (new EditorGUILayout.HorizontalScope())
            {
                if (GUILayout.Button("Send a test event"))
                    Analytics.Track("playersense_editor_test", new EventProperties
                    {
                        ["source"] = "editor_window",
                    });

                if (GUILayout.Button("Flush now")) SDK.Flush();
            }

            EditorGUILayout.HelpBox(
                "Counters are this session only. They are what the SDK did, not what the server " +
                "stored — \"Accepted rows\" is the first number here that the server agreed with.",
                MessageType.None);

            EditorGUILayout.EndScrollView();
        }

        private static void Row(string label, string value, string tooltip = null)
        {
            EditorGUILayout.LabelField(new GUIContent(label, tooltip), new GUIContent(value));
        }

        /// <summary>A counter that should be zero, drawn so that non-zero is visible.</summary>
        private static void Warn(string label, ulong value, string tooltip)
        {
            var colour = GUI.color;
            if (value > 0) GUI.color = new Color(1f, 0.72f, 0.2f);
            EditorGUILayout.LabelField(new GUIContent(label, tooltip), new GUIContent(value.ToString()));
            GUI.color = colour;
        }
    }
}
