// PlayerSense.Privacy — what the player has agreed to.
//
// Nothing about buffers, spools, batch boundaries or promotion appears here. The SDK handles
// all of it: events collected before an answer stay in memory and never touch the disk,
// granting makes them lawful and sends them, denying discards them, and a withdrawal flushes
// what was lawfully collected before it stops.

using System;
using PlayerSense.Core;

namespace PlayerSense
{
    /// <summary>What the player has said about analytics collection.</summary>
    public enum AnalyticsConsent
    {
        /// <summary>They have not answered. Events are held in memory only.</summary>
        Unknown,
        Granted,
        Denied,
    }

    /// <summary>Apple's App Tracking Transparency status. Recorded, never acted on.</summary>
    public enum AttStatus
    {
        NotDetermined,
        Restricted,
        Denied,
        Authorized,
    }

    public sealed class PrivacyModule
    {
        private readonly PlayerSenseClient _client;

        internal PrivacyModule(PlayerSenseClient client) => _client = client;

        /// <summary>Records the player's answer.</summary>
        /// <param name="consent">What they said.</param>
        /// <param name="policyVersion">
        /// Which version of your terms they agreed to. Required for a grant: it is what makes
        /// a later withdrawal scopeable to "everything collected under version N or earlier"
        /// rather than to everything.
        /// </param>
        /// <param name="att">Apple's tracking status, where you have it.</param>
        /// <param name="gdprApplies">Whether you consider this player in scope for GDPR.</param>
        public unsafe void SetConsent(AnalyticsConsent consent, string policyVersion,
                                      AttStatus att = AttStatus.NotDetermined,
                                      bool gdprApplies = true)
        {
            if (consent == AnalyticsConsent.Denied || consent == AnalyticsConsent.Unknown)
            {
                // Unknown is the state a client starts in; moving *back* to it is not a thing
                // the protocol models, so it is treated as a refusal — the safe reading.
                PlayerSenseClient.Check(
                    Native.ps_client_set_consent_denied(_client.Handle), nameof(SetConsent));
                return;
            }

            if (string.IsNullOrEmpty(policyVersion))
            {
                UnityEngine.Debug.LogError(
                    "[PlayerSense] SetConsent(Granted) needs a policy version. Without one, a " +
                    "later withdrawal cannot be scoped to anything narrower than everything.");
                return;
            }

            var scratch = _client.Scratch;
            scratch.Reset();
            var policy = scratch.Write(policyVersion);
            var status = scratch.Write(Describe(att));

            fixed (byte* origin = scratch.Buffer)
            {
                PlayerSenseClient.Check(
                    Native.ps_client_set_consent_granted(
                        _client.Handle,
                        policy.Resolve(origin),
                        status.Resolve(origin),
                        gdprApplies ? 1 : 0,
                        DateTimeOffset.UtcNow.ToUnixTimeMilliseconds()),
                    nameof(SetConsent));
            }
        }

        /// <summary>Records that the player agreed, under a named version of your terms.</summary>
        public void Grant(string policyVersion, AttStatus att = AttStatus.NotDetermined,
                          bool gdprApplies = true) =>
            SetConsent(AnalyticsConsent.Granted, policyVersion, att, gdprApplies);

        /// <summary>Records that the player refused, or withdrew.</summary>
        public void Deny() => SetConsent(AnalyticsConsent.Denied, null);

        // ---- error reports ------------------------------------------------------------
        //
        // A second question, for games that ask it. By default error reports follow the
        // analytics answer: one consent screen, one answer. A game with a separate "Share error
        // reports" switch in its settings, the way desktop apps ask, records that answer here
        // and it wins over the analytics one for logs and crashes, in either direction.

        /// <summary>Records that the player agreed to share error reports, whatever they said about analytics.</summary>
        /// <param name="policyVersion">The version of your terms they agreed to, as for <see cref="Grant"/>.</param>
        public unsafe void GrantErrorReports(string policyVersion, AttStatus att = AttStatus.NotDetermined,
                                             bool gdprApplies = true)
        {
            if (string.IsNullOrEmpty(policyVersion))
            {
                UnityEngine.Debug.LogError(
                    "[PlayerSense] GrantErrorReports needs a policy version, for the same reason " +
                    "Grant does.");
                return;
            }

            var scratch = _client.Scratch;
            scratch.Reset();
            var policy = scratch.Write(policyVersion);
            var status = scratch.Write(Describe(att));

            fixed (byte* origin = scratch.Buffer)
            {
                PlayerSenseClient.Check(
                    Native.ps_client_set_diagnostics_granted(
                        _client.Handle,
                        policy.Resolve(origin),
                        status.Resolve(origin),
                        gdprApplies ? 1 : 0,
                        DateTimeOffset.UtcNow.ToUnixTimeMilliseconds()),
                    nameof(GrantErrorReports));
            }
        }

        /// <summary>
        /// Records that the player refused error reports. Any crash waiting on the device for
        /// their answer is deleted.
        /// </summary>
        public void DenyErrorReports() =>
            PlayerSenseClient.Check(
                Native.ps_client_set_diagnostics_denied(_client.Handle), nameof(DenyErrorReports));

        /// <summary>Error reports follow the analytics answer again. The default.</summary>
        public void ErrorReportsFollowAnalytics() =>
            PlayerSenseClient.Check(
                Native.ps_client_set_diagnostics_follow_analytics(_client.Handle),
                nameof(ErrorReportsFollowAnalytics));

        /// <summary>
        /// How many crash reports are waiting on the device for the player's answer.
        /// </summary>
        /// <remarks>
        /// For a game that asks at the right moment: "The game closed unexpectedly last time.
        /// Send a report?" Then <see cref="GrantErrorReports"/> sends them, and
        /// <see cref="DiscardPendingCrashReports"/> or <see cref="DenyErrorReports"/> does not.
        /// </remarks>
        public int PendingCrashReports =>
            Native.ps_client_held_crash_reports(_client.Handle, out long count) == Native.Ok
                ? (int)count
                : 0;

        /// <summary>Deletes the crash reports waiting on the device, without changing any answer.</summary>
        public void DiscardPendingCrashReports() =>
            PlayerSenseClient.Check(
                Native.ps_client_discard_held_crash_reports(_client.Handle),
                nameof(DiscardPendingCrashReports));

        private static string Describe(AttStatus att) => att switch
        {
            AttStatus.Authorized => "authorized",
            AttStatus.Denied => "denied",
            AttStatus.Restricted => "restricted",
            _ => "not_determined",
        };
    }
}
