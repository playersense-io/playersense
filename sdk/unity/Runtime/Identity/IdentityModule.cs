// PlayerSense.Identity — who the player is, in your terms.

using PlayerSense.Core;

namespace PlayerSense
{
    public sealed class IdentityModule
    {
        private readonly PlayerSenseClient _client;

        internal IdentityModule(PlayerSenseClient client) => _client = client;

        /// <summary>Attaches your own identifier to every subsequent event.</summary>
        /// <remarks>
        /// Does not flush and does not restart the session. The identifier travels per event,
        /// so a batch that straddles a sign-in is correct — the events before it carry no
        /// actor and the events after carry one. Call it whenever your login completes.
        /// </remarks>
        public unsafe void SetPlayer(string playerId)
        {
            var scratch = _client.Scratch;
            scratch.Reset();
            var span = scratch.Write(playerId);

            fixed (byte* origin = scratch.Buffer)
            {
                PlayerSenseClient.Check(
                    Native.ps_client_set_player(_client.Handle, span.Resolve(origin)),
                    nameof(SetPlayer));
            }
        }

        /// <summary>Detaches the actor. Subsequent events are anonymous again.</summary>
        public void ClearPlayer() => SetPlayer(null);

        /// <summary>
        /// The installation this device is known by. A reinstall produces a new one, by design.
        /// </summary>
        public string InstallId => _client.InstallId;
    }
}
