# 0035. An invitation is addressed, not merely held

**Status:** Accepted
**Date:** 2026-09-18
**Relates to:** project.md §11, §55.1, §99; [ADR 0008](0008-two-tier-control-plane.md), [ADR 0011](0011-api-key-format-and-storage.md), [ADR 0031](0031-absence-is-null-not-the-empty-string.md)

## Context

The authorization model shipped with six seeded roles, a project access list, a permission
vocabulary of twenty-two names and the two invariants SQL cannot express — an organization keeps
one owner, and a member cap that is a ceiling rather than a lock. `identity` held `AddMember`,
`RemoveMember`, `SetMemberRole` and the invitation token helpers. The `Invitation`,
`OrganizationMember`, `Role` and `MemberProject` tables existed.

**No route could admit a second person.** `member.read`, `member.invite` and `member.remove`
reached nothing. The RBAC suite wrote membership rows itself through a helper commented *"which is
what the invitation flow will do once it exists"*. Every organization in the product was one
person, permanently, and the role system existed to distinguish between people who could not be
added.

That left one design question genuinely open, and it is not the one it looks like.

## Decision

### The address is the authorisation; the token is how a link works

An invitation can be accepted **two ways**: with the token from the link, or by naming the
organization when one is already waiting for the caller's verified address.

The narrow reading — the token is the credential, possession of the message is the check — is the
one the schema was written under, and it is wrong in a way that costs a real person. Firebase has
vouched for the address on the request; the invitation was sent to exactly that address. A token
establishes no further fact. What requiring it adds is a **dead end**: somebody who deleted the
mail, or whose client mangled the link, can see on their dashboard that they were invited and
cannot act on it, and the remedy is "ask an administrator to revoke and re-send".

The case the narrow reading protects against is an address Firebase considers verified that the
person does not control. That is a Firebase compromise, and an attacker holding it can read the
inbox — so the token does not survive the threat it would be defending against.

What *is* load-bearing, and is enforced: the accepting account's **verified** address must equal the
address the invitation was sent to, compared case-insensitively. A forwarded link does not let a
colleague join in somebody else's place, with somebody else's role.

### Accepting needs `RequireVerified`, because it has no permission to withhold

The accept route names no organization, so there is no membership, no role and nothing a permission
could be checked against. That makes it the second route in the product — after creating an
organization — that a read-only account would otherwise walk through, because `Actor.ReadOnly` can
only withhold permissions that are *checked*. An address Firebase has not confirmed accepting an
invitation sent to that address is precisely how somebody joins an organization they were never
invited to.

Listing what is waiting for you needs no verification: reading what was sent to your own address
changes nothing, and it is how an unverified account learns which link to look for.

### Role and access changes are guarded by system role, not by permission

`RequireRoleManagement` rather than `core.Require`. A grantable `role.manage` is silently
equivalent to owner, because whoever holds it can write themselves a role containing everything —
so the escalation question is removed rather than defended against.

Muzak keeps one `Authorized` provider per route, so the two guards cannot be combined. That
constraint is useful: every route has exactly one answer to "who may do this", and it is why
changing a role and changing project access are two routes rather than one request with two guards.

### The two axes are two requests, and `all_projects` is required

A role says what; the access list says where. `all_projects` is the widest grant in the product, so
it is a required member of its own request rather than a field on a combined member write — where
forgetting to echo it back would widen or narrow somebody's reach silently, with a 200. This is the
same footgun [ADR 0034](0034-a-rule-is-validated-when-it-is-written.md) closed for `archived`, and
it is closed the opposite way here because the two mean different things: an archive state should be
*preserved* when unmentioned, and an access grant should be *stated*.

An unknown project slug refuses the whole request. "Give this contractor these three games"
silently granting two is exactly what an access review exists to catch and would not.

### Removing a member deletes the row

The one place this codebase deletes rather than archives, and the exception is worth stating
because the instinct after revoked keys and archived flags is to keep it. A membership is not a
record of what happened; it is a live grant of access to a studio's player data. Keeping a disabled
one would make the answer to "who can reach this" depend on a boolean somebody has to remember to
read. §08's access review asks who *can* reach the data, and that is exactly the set of rows that
remain.

This does leave a real gap, stated rather than papered over: **the removal itself is not recorded.**
The `audit_events` table exists and nothing writes it — see
[docs/compliance/10-open-items.md](../compliance/10-open-items.md) — so today a membership that is
gone leaves no trace of who ended it or when. The alternative on offer was not "keep the audit
trail" but "keep a disabled grant and call it one", which answers a different question badly.

An **invitation** is kept and marked revoked, because the opposite is true of it: it is a record of
how somebody got in, and the digest has to stay so the token keeps matching something that refuses
it rather than matching nothing by luck.

### Sending is part of the write, and it is rate limited

A failed send rolls the row back. A 201 carrying an invitation that was never sent is a row on the
members screen that never arrives and cannot be re-sent, because the address is now already
invited.

Invitations are the **only rate-limited route on the dashboard**, which is otherwise deliberately
unlimited. The budget protects somebody who is not making the request: a stranger's attention, this
deployment's sending reputation, and eventually money. Three per address and thirty per organization
per hour, and it **fails closed** — every SDK-path budget fails open because §65 says losing Valkey
costs speed rather than data, and a message cannot be taken back.

### One outstanding invitation per address per organization

Not a rate limit. Two live tokens for one inbox would mean revoking "the" invitation leaves the
other working.

### Unknown, spent, revoked and expired are one message

Distinguishing them tells somebody holding a token whether it was ever real, and the only person
who needs that distinction is one who should not have the token. An elapsed invitation is *reported*
as expired without anything sweeping the table, because the status column is written only when a row
is touched.

## Consequences

**A person can be in an organization they did not choose to join**, if somebody invited their
address and they sign in with it — one request, no link. That is what an invitation means, and the
member list makes it visible to the organization; what it does not do is let them be added without
an invitation existing first.

**Nothing is emailed.** `MAIL_SENDER=log` composes and delivers nothing. The response carries
`delivered: false` and the log line says so, because a dashboard telling somebody to check their
inbox for a message that was only logged is a lie the product would be telling on a studio's
behalf. The body carries the token, so it is logged only under `MAIL_LOG_BODY=true` — a token in a
log outlives the invitation's expiry and is readable by people who were never invited.

**The backend composes a link to a front end that does not exist.** `DASHBOARD_URL` is
configuration because being wrong about it produces an invitation nobody can accept.

**A whole-object member write does not exist**, so a dashboard makes two requests to change a
role and an access list. The alternative was one request in which the widest grant in the product
could be given by omission.

## Alternatives considered

**Token only.** Rejected above: it establishes nothing the verified address does not, and it leaves
a person able to see an invitation they cannot accept.

**Address only, with no token.** Rejected because the token is what makes a *link* work — one click
from the inbox, including before the account exists. Both, with one required, costs one branch.

**Approve join requests instead of inviting.** Rejected: an organization's data is a studio's
players, and a request somebody has to remember to reject is one that eventually gets accepted by
mistake. The schema's own comment says this and it stands.

**A `role.manage` permission.** Rejected as equivalent to owner. Reserving it to the system roles
removes the question.

**Keeping a removed membership as a disabled row.** Rejected: see above. It converts an access list
into an access list plus a boolean, and the boolean is what gets missed.

**Argon2id for the token digest.** Rejected, and this is where an invitation differs from an API
key ([ADR 0011](0011-api-key-format-and-storage.md)): a KDF exists to slow an attacker against a
secret a *person* chose. This is 256 bits from `crypto/rand`, so there is nothing to brute-force and
no reason to pay a KDF on every accept.

**No rate limit, consistent with the rest of the dashboard.** Rejected because the argument for the
dashboard's freedom is that a limit there can only inconvenience a customer — which stops being true
the moment a request spends somebody else's attention.

## Enforcement

`internal/routers/members.go` mounts the routes; `internal/handlers/members.go` and
`internal/identity/invitation.go` hold the behaviour; `internal/mail` is the seam that makes the
send observable; `core.InvitationBudgets` is the one dashboard budget.

In `apps/backend/cmd/control-api/members_test.go`:

| Test | Fails when |
|---|---|
| `TestAnInvitationAdmitsASecondPerson` | anything in the invite → accept → listed path breaks |
| `TestAForwardedInvitationDoesNotLetSomebodyElseIn` | the address check is dropped |
| `TestAnUnverifiedAccountCannotAcceptAnInvitation` | `RequireVerified` leaves the accept route |
| `TestAnInvitationCanBeAcceptedWithoutTheEmail` | the token becomes the only way in, or naming both is resolved by precedence |
| `TestAcceptingTwiceSucceeds` | a second click becomes an error |
| `TestAnExpiredOrRevokedInvitationIsIndistinguishable` | a token can report on itself, or a revoked row is deleted |
| `TestAnOrganizationKeepsAnOwner` | either door lets the last owner go |
| `TestProjectAccessIsTheSecondAxis` | `all_projects` becomes optional, an unknown slug is dropped silently, or widening keeps stale rows |
| `TestRoleManagementIsNotAPermission` | assigning a role or widening access becomes an ordinary permission |
| `TestAnInvitationIsRefusedForSomebodyAlreadyIn` | a member or an outstanding address can be re-invited |
| `TestTheInvitationSaysWhatItIsAndWhoSentIt` | the message stops naming the inviter, the role or the link, or a placeholder claims delivery |
| `TestAnInboxCannotBeFlooded` | the invitation budget stops counting, or stops naming which budget refused |
| `TestRemovingAMemberDeletesTheGrant` | a removal leaves a row behind |
| `TestAnotherStudiosMembersAreInvisible` | this surface answers 403 where it should answer 404 |
| `TestTheSeededRolesAreListedWithWhatTheyGrant` | a system role's permissions are read from its column instead of computed |
