// PlayerSense.Privacy — what the player has agreed to.

namespace PlayerSense
{
    /// <summary>The player's answer about analytics collection.</summary>
    /// <remarks>
    /// Nothing about buffers, batches or promotion is your problem. Events collected before an
    /// answer are held in memory and never touch the disk; granting makes them lawful and sends
    /// them; denying discards them; withdrawing sends what was lawfully collected and stops.
    /// </remarks>
    public static class Privacy
    {
        /// <summary>Records that the player agreed.</summary>
        /// <param name="policyVersion">
        /// Which version of your terms they agreed to — <c>"2026.09"</c>, <c>"3"</c>, whatever
        /// you version them by. Required: it is what lets a later withdrawal be scoped to
        /// "everything collected under version N or earlier" rather than to everything.
        /// </param>
        /// <param name="att">Apple's tracking status, where you have it. Recorded, never acted on.</param>
        /// <param name="gdprApplies">Whether you consider this player in scope for GDPR.</param>
        public static void Grant(string policyVersion, AttStatus att = AttStatus.NotDetermined,
                                 bool gdprApplies = true)
        {
            var module = SDK.Client?.Privacy;
            if (module == null)
            {
                SDK.WarnNotInitialized("Privacy.Grant");
                return;
            }
            module.Grant(policyVersion, att, gdprApplies);
        }

        /// <summary>Records that the player refused, or withdrew.</summary>
        public static void Deny()
        {
            var module = SDK.Client?.Privacy;
            if (module == null)
            {
                SDK.WarnNotInitialized("Privacy.Deny");
                return;
            }
            module.Deny();
        }

        /// <summary>
        /// Records the answer from a variable, for a settings screen that holds it as state.
        /// </summary>
        public static void SetConsent(AnalyticsConsent consent, string policyVersion,
                                      AttStatus att = AttStatus.NotDetermined,
                                      bool gdprApplies = true)
        {
            var module = SDK.Client?.Privacy;
            if (module == null)
            {
                SDK.WarnNotInitialized("Privacy.SetConsent");
                return;
            }
            module.SetConsent(consent, policyVersion, att, gdprApplies);
        }

        /// <summary>Records that the player agreed to share error reports, whatever they said about analytics.</summary>
        /// <remarks>
        /// For a "Share error reports" switch of its own, the way desktop apps ask. Without one,
        /// error reports follow the analytics answer and you need none of this.
        /// </remarks>
        public static void GrantErrorReports(string policyVersion, AttStatus att = AttStatus.NotDetermined,
                                             bool gdprApplies = true)
        {
            var module = SDK.Client?.Privacy;
            if (module == null)
            {
                SDK.WarnNotInitialized("Privacy.GrantErrorReports");
                return;
            }
            module.GrantErrorReports(policyVersion, att, gdprApplies);
        }

        /// <summary>Records that the player refused error reports. Waiting crash reports are deleted.</summary>
        public static void DenyErrorReports()
        {
            var module = SDK.Client?.Privacy;
            if (module == null)
            {
                SDK.WarnNotInitialized("Privacy.DenyErrorReports");
                return;
            }
            module.DenyErrorReports();
        }

        /// <summary>Error reports follow the analytics answer again. The default.</summary>
        public static void ErrorReportsFollowAnalytics()
        {
            var module = SDK.Client?.Privacy;
            if (module == null)
            {
                SDK.WarnNotInitialized("Privacy.ErrorReportsFollowAnalytics");
                return;
            }
            module.ErrorReportsFollowAnalytics();
        }

        /// <summary>How many crash reports are waiting on the device for the player's answer.</summary>
        public static int PendingCrashReports => SDK.Client?.Privacy.PendingCrashReports ?? 0;

        /// <summary>Deletes the crash reports waiting on the device, without changing any answer.</summary>
        public static void DiscardPendingCrashReports() => SDK.Client?.Privacy.DiscardPendingCrashReports();
    }
}
