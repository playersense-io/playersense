# Rate limits

**One player's behaviour must never cost another player their data.**

That sentence is the whole design. Everything below is a consequence of it, and any change
that breaks it is wrong however sensible it looks on its own.

---

## 1. Where limits are, and where they are not

| surface | limited by | why |
|---|---|---|
| `ingest-api` | device, address-in-project, project | a runaway device must not spend the game's budget |
| `config-api` | device, address-in-project, project | the same, at a tenth of the volume |
| `control-api` | **one route only**: sending an invitation, per address and per organization | the cost lands on somebody who is not making the request |
| `query-api` | **nothing** | every query is already bounded individually |

One of those is an absence, and it is deliberate. The other is a single exception that proves
the rule rather than a retreat from it.

### The dashboard is not rate limited

`control-api` is authenticated, low-volume, and used by people rather than programs. A
studio has tens of employees, not thousands, and a person clicking around a dashboard cannot
generate load worth defending against. A rate limit there has exactly one observable effect:
one day it refuses a customer who did nothing wrong.

What it would have protected is not worth protecting. Nothing expensive happens before a
sign-in is verified — a request carrying a bad token costs one RSA verification against a
cached key (**96µs**, measured) and no database work at all, because the verification fails
before any lookup. An unauthenticated flood is a problem for the platform the service runs
on, which already has one; it is not a problem a per-caller budget solves.

### Except for the one route whose cost is somebody else's

Sending an invitation is the only write in the control plane that reaches outside the system.
The person it can hurt is not the person making the request: it costs a stranger's attention,
this deployment's sending reputation, and — once a provider is configured — money. That is a
different argument from load, and it is the entire reason this surface has one budget when
everything else about it deliberately has none.

| budget | limit | derivation |
|---|---|---|
| per address | **3 / hour** | a studio inviting one colleague sends one message, and a second if the first went to a typo. Past that it is not filling a seat |
| per organization | **30 / hour** | a whole team is onboarded in a sitting. Thirty covers a studio larger than the member cap allows, so reaching it means something other than onboarding |

The address budget is counted first, so the refusal names the address — which is the thing the
sender can act on by checking the spelling — rather than telling them their organization is busy.
Both are counted even when the first is over, for the reason every other budget here is: a
counter that stops being incremented stops being a measurement.

Two spellings of one inbox share a counter, because a limit anybody can lift by pressing shift
is not a limit.

**This one fails closed.** Every SDK-path limit fails open, because §65's rule is that losing
Valkey costs speed rather than data — right when the thing at risk is a batch of events. A
message cannot be taken back, so when the counter cannot be read the answer is no.

There is a second, separate rule beside the budget: **one outstanding invitation per address per
organization.** Not a rate limit at all — two live tokens for one inbox would mean revoking "the"
invitation leaves the other one working.

### Queries are bounded, not counted

`query-api` enforces §32 per *query* rather than per caller: `MaxRows`, `MaxExecutionTime`,
`MaxRowsToRead`, `MaxBytesToRead`, the compiler's structural refusal to join on an actor key,
and §32.2's minimum cell size. Those are the limits that matter — **a query that cannot be
expensive cannot be made catastrophic by repetition.** A request limit on top would only
ever slow down a real dashboard, which is the one caller this service has.

That holds for both of its credentials. A reporting key and a signed-in person reach the same
planner under the same bounds (§55.2), so the reasoning does not change when the caller is a
browser rather than a studio's BI job — a person cannot ask a cheaper query than a key can, and
neither can ask an unbounded one.

---

## 2. What the SDK paths count

Four budgets per request, narrowest first. The order is the error message: the first breach
is what the caller is told, so a looping device is told it is the device rather than being
told its project is busy — which would send a developer to look at the wrong thing.

```
ingest-api                                     config-api
  device-burst          20 / second              device-config-burst       10 / second
  device-sustained     120 / minute              device-config-sustained   60 / minute
  address-sustained  12,000 / minute             address-config-sustained   3,000 / minute
  project-sustained 120,000 / minute             project-config-sustained  30,000 / minute
```

### Where the numbers come from

A well-behaved installation flushes every 30 seconds (§7) and asks for its configuration
every fifteen minutes. Counting a session start and a purchase going immediately, that is
**at most three requests a minute** on ingestion and **four an hour** on configuration.

The device budgets are an order of magnitude above that. The only device that reaches one is
a device that is looping — a retry without backoff, a `Refresh()` in `Update`, a test
harness left running. That is exactly the population the limit is for.

The address budget is sized for a **shared network**: an office, a university, a LAN party.
It is deliberately *not* sized for carrier-grade NAT, which the project budget covers
instead — see §3.

The project budget is sized at **capacity**, not at a plan's worth of traffic. It is the
platform's own protection and the one budget whose breach affects players who did nothing
wrong, so it is set where reaching it means something is genuinely wrong rather than that a
studio is popular.

### Why the device budget is the one meant to bite

It is the only budget whose subject is one player. When it refuses, exactly one installation
is affected and it is the one misbehaving. Every wider budget, when it refuses, catches
bystanders — so the design is arranged so that the wider ones are not reached.

---

## 3. The bug this replaced

Until this change every service used the framework's own limiter, keyed on the client's
address:

```go
Tracker: muzak.IPPrefixTracker(32, 64)   // "ip:" + prefix, and nothing else
```

Two things were wrong with it, and the second is the serious one.

**An address is not a player.** Carrier-grade NAT routinely puts tens of thousands of
subscribers behind a single IPv4. Against ingestion's `6000/min`, and a device sending about
two requests a minute:

```
6000 ÷ 2  =  ~3,000 devices behind one address before the limit binds
```

In markets where CGNAT ratios of 1000:1 are ordinary — India, Indonesia, Nigeria, much of
South-East Asia — a moderately popular game would have been throttling real players. It
degrades rather than loses, because the SDK backs off and spools for up to seven days, so it
would have surfaced as unexplained ingestion lag rather than as an error anyone reported.

**The key carried no project.** Every customer behind one carrier NAT shared one budget, so
one studio's hit game could throttle a different studio's players. That is not a limit, it is
a coupling between customers who have nothing to do with each other.

IPv6 was never affected: a `/64` is what a provider hands one subscriber.

---

## 4. Why the limits are counted where they are

The framework's limiter runs **before any dependency resolves** (muzak's documented order:
middleware, rate limit, guards, providers, binding, handler). At that point the request is an
address and a byte stream — the project has not been resolved from the credential and the
installation has not been parsed from the body. Keying on anything meaningful is therefore
impossible there.

Muzak offers `AfterDependencies: true`, which moves counting after the providers so a tracker
can read a resolved value. It was rejected: a request rejected *before* the user resolves
would then not be counted at all, so an attacker spraying invalid credentials would get
unlimited free verifications. One policy carries one tracker, so the two cannot coexist.

So the meaningful budgets are counted in the **handler**, which is the first point where both
the project (from the credential) and the device (from the body) are known. They run before
the residency check on purpose: a batch aimed at the wrong cell is still a request this cell
served, and not counting it would make the 421 path the cheapest way to send unlimited
traffic.

All four counters go out in **one Lua round trip** (`valkey.RateLimitStorage.IncrementMany`).
Four round trips per request on the ingestion path would make the limiter more expensive than
the work it protects.

Every budget is counted even when an earlier one is already over. Stopping at the first would
make the later counters depend on the order the keys happened to be passed in, so a device
over its own limit would stop contributing to its project's — and the project's number would
quietly become a number about something else.

---

## 5. When Valkey cannot count

Both ingestion and configuration **allow** requests when rate-limit storage fails. §65's
rule is that losing Valkey costs speed rather than data: refusing every batch or configuration
request because a cache is unreachable would turn a degraded dependency into a product outage.

The storage reports an error, and the SDK limiter's `FailOpen` policy allows the request.
The limiter also supports reporting that error as a 503 when `FailOpen` is false, but no
service currently registers that policy. Authentication still requires a valid credential.

---

## 6. Firebase owns authentication email sending

The dashboard calls Firebase's browser SDK directly for authentication emails. The Go
backend verifies ID tokens and enforces permissions; resolving a user or reading a dashboard
does not send email. There is no duplicate email budget in Valkey and no backend Web API key
or Identity Toolkit URL setting.

**An invitation is not an authentication email**, which is why it is the one message PlayerSense
sends itself (`internal/mail`). Firebase cannot send it: the message is about an organization, and
Firebase does not know what one is. It carries no credential for an account — only a token for a
membership — so none of the reasoning above applies to it, and it has a budget of its own (§1).

No provider is configured, so `MAIL_SENDER=log` composes the message and delivers nothing, saying
so in the line it writes and in the `delivered` member of the response. The body carries the
token, so it is logged only under `MAIL_LOG_BODY=true` — a development setting, because a token in
a log outlives the invitation's expiry and is readable by people who were never invited.

The dashboard is not implemented yet. Its authentication email behavior is specified here:

| action | Firebase browser SDK method | trigger |
|---|---|---|
| Send confirmation | `sendEmailVerification(user)` | after email/password signup |
| Resend confirmation | `sendEmailVerification(user)` | an explicit resend action |
| Forgot password / request password reset | `sendPasswordResetEmail(auth, email)` | an explicit recovery action |
| Passwordless sign-in | `sendSignInLinkToEmail(auth, email, settings)` | an explicit sign-in action |

These are Firebase's documented [user-management](https://firebase.google.com/docs/auth/web/manage-users)
and [email-link authentication](https://firebase.google.com/docs/auth/web/email-link-auth) flows.
An email-link sign-in already verifies the address; it needs no additional confirmation email.

Disable the sending button while its request is pending. After Firebase accepts a request,
apply a **60-second button cooldown** before another send. This is PlayerSense's interface
default, not a Firebase-prescribed interval or a security limit. A browser control can be
bypassed; Firebase's servers enforce [sending quotas and abuse protections](https://firebase.google.com/docs/auth/limits).
Firebase acceptance confirms the request, not delivery to the inbox.

Show provider throttling as a temporary refusal and ask the person to try again later. Do not
interpret `auth/too-many-requests` as evidence that a previous email was delivered. Other
failures should offer a retry without imposing the cooldown for a successful send.

Enable Firebase's [email enumeration protection](https://firebase.google.com/support/guides/security-checklist)
for password recovery and use a generic response such as "If an account exists for this
address, a password reset email has been requested." Do not disclose account existence.

After confirmation, reload the Firebase user and force an ID-token refresh with
`getIdToken(user, true)` before sending further backend requests. The backend uses the
verified claim in that token to permit protected writes. Unverified accounts can still read
the resources they have access to.

Organisation invitations are a separate, future server-owned email flow. Sending and abuse
limits belong on the backend when that flow is implemented; no invitation quota is enforced
or specified today.

---

## 7. What a refused caller sees

```http
HTTP/1.1 429 Too Many Requests
Retry-After: 4

{"error":{"code":"rate_limited",
          "message":"over the device-burst limit of 20 per 1s; try again in 4s"}}
```

`Retry-After` is a real header and is never zero — a client told to wait no time retries
immediately, which is the request that was just refused. The SDK reads it, backs off, and
spools; the message names the budget so a person reading a log can tell a looping device from
a project over its ceiling.

**A 429 delays data; it does not lose it.** The SDK holds the batch on disk and retries for up
to seven days (§19.2's skew window). Data is only lost if a limit is breached continuously for
longer than that, which for the device budget would mean one installation sending for a week
without pause.

---

## 8. The case where a bystander is affected, stated plainly

If a project reaches its own ceiling, every device in that game is refused — including the
well-behaved ones. There is no way around it: protecting the platform and never affecting an
innocent player are genuinely in tension once a single customer exceeds capacity.

Three things make it acceptable rather than merely tolerated:

- The ceiling is set at **capacity**, not at a plan's allowance, so reaching it means
  something is wrong rather than that a game did well.
- The device budget bites first, so the runaway case — which is most cases — never reaches it.
- A 429 is a delay, not a loss. The affected players' data arrives late rather than never.

If this is ever reached in production it is an incident, not a limit doing its job.

---

## 9. What is deliberately not built

**Per-plan limits.** The billing schema has entitlements and these could be among them. They
are not, because these limits exist to protect the service rather than to price it, and making
them a commercial lever would mean every request resolving a project's plan before it can be
counted.

**Per-organisation quotas.** §32 asked for them. They are the opposite of this document's
first sentence — a shared budget is precisely the mechanism by which one person's usage
affects another's — and they have been struck from the spec rather than left as an unbuilt
item that contradicts the code.

**Concurrency limits**, as distinct from rate limits. A request in flight is not counted; only
requests started are. `query-api`'s per-query resource bounds are what stops a slow query
holding a connection, and they are the right tool for it.
