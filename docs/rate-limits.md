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
| `control-api` | **nothing**, except email | a limit here can only inconvenience a customer |
| `query-api` | **nothing** | every query is already bounded individually |

Two of those are absences, and both are deliberate.

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

### Queries are bounded, not counted

`query-api` enforces §32 per *query* rather than per caller: `MaxRows`, `MaxExecutionTime`,
`MaxRowsToRead`, `MaxBytesToRead`, the compiler's structural refusal to join on an actor key,
and §32.2's minimum cell size. Those are the limits that matter — **a query that cannot be
expensive cannot be made catastrophic by repetition.** A request limit on top would only
ever slow down a real dashboard, which is the one caller this service has.

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

## 5. Failing open, and failing closed

When Valkey cannot be reached, the limiter cannot count. What happens next differs by
surface, and the asymmetry is deliberate.

| surface | on failure | why |
|---|---|---|
| ingestion, configuration | **allow** | §65: losing Valkey costs speed, not data. Refusing every batch in the product because a cache is unreachable turns a degraded dependency into an outage of the thing customers pay for. |
| email | **refuse** | An email reaches a real person's inbox and cannot be taken back. When we cannot count, we do not send. |

---

## 6. Email is limited, and not because of load

A verification link, an invitation and a password reset each cost money, cost sender
reputation, and arrive in **somebody else's inbox** whether or not they asked. The person
being protected is not the one making the request, which is a different argument from every
other limit in this document — and it is why these survive on a dashboard that is otherwise
unlimited.

| budget | window | limit |
|---|---|---|
| per address | 15 minutes | 1 |
| per organisation | 1 hour | 60 |

**Per address** is the one that matters, because the address belongs to whoever is being
written to. Nobody needs the same link twice in a quarter of an hour, and a person who did
not receive the first will not receive the second — the problem is somewhere else.

**Per organisation** stops a studio inviting a thousand strangers in an afternoon. A mailbox
provider does not distinguish an enthusiastic onboarding from a spam run, and the consequence
— a domain on a blocklist — lands on every customer at once.

Firebase applies a limit of its own, much tighter: measured against a real project, the
request immediately after a successful send is refused with `TOO_MANY_ATTEMPTS_TRY_LATER`.
Without our own throttle in front, a person reloading the dashboard would spend that
allowance in seconds and then be told the send failed — for the one thing that had worked.

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
