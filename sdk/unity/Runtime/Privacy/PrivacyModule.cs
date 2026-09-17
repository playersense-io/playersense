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

        private static string Describe(AttStatus att) => att switch
        {
            AttStatus.Authorized => "authorized",
            AttStatus.Denied => "denied",
            AttStatus.Restricted => "restricted",
            _ => "not_determined",
        };
    }
}
