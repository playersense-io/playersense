// The one MonoBehaviour in the package.
//
// It exists for three things Unity will not do without an object in the scene: give the SDK a
// chance to do background work, tell it when the app comes and goes, and run the web requests.
//
// DontDestroyOnLoad and hidden in the hierarchy, because scene loads — additive, single, a
// reload during play — are exactly the lifecycle events studios forget to handle, and the
// protocol's whole posture is that PlayerSense owns them rather than asking.

using UnityEngine;

namespace PlayerSense
{
    [AddComponentMenu("")]
    internal sealed class PlayerSenseRunner : MonoBehaviour
    {
        private PlayerSenseClient _client;
        private float _interval = 0.25f;
        private float _next;

        internal static PlayerSenseRunner Create(PlayerSenseClient client, float tickIntervalSeconds)
        {
            var go = new GameObject("PlayerSense")
            {
                hideFlags = HideFlags.HideAndDontSave,
            };
            DontDestroyOnLoad(go);

            var runner = go.AddComponent<PlayerSenseRunner>();
            runner._client = client;
            runner._interval = Mathf.Max(0.05f, tickIntervalSeconds);
            return runner;
        }

        private void Update()
        {
            // On an interval rather than every frame. Nothing here is urgent — the core sends a
            // session start or a purchase the moment it is tracked — and a per-frame P/Invoke
            // for work that is usually nothing is a cost a game can feel.
            if (Time.unscaledTime < _next) return;
            _next = Time.unscaledTime + _interval;
            _client?.Tick();
        }

        private void OnApplicationPause(bool paused)
        {
            if (_client == null) return;
            if (paused) _client.Background();
            else _client.Foreground();
        }

        private void OnApplicationFocus(bool focused)
        {
            // Desktop reports focus rather than pause, so both are wired. The core is
            // idempotent about either, so a platform that reports both is not a problem.
            if (_client == null) return;
            if (focused) _client.Foreground();
            else _client.Background();
        }

        private void OnApplicationQuit()
        {
            // The last moment this is guaranteed to run. The core spools before it attempts the
            // network, so whatever does not go now goes on the next launch.
            SDK.Shutdown();
        }
    }
}
