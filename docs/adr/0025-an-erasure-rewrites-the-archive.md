# 0025. An erasure rewrites an archive object rather than deleting it

**Status:** Accepted
**Date:** 2026-09-17
**Relates to:** project.md §14.1, §21, §34, §59, §60, §61, §101, Appendix C.2; [ADR 0022](0022-an-archive-object-is-named-by-its-contents.md)

## Context

§61 asks an erasure to "rewrite or remove affected archive objects during a
compaction/deletion job" and leaves the choice between the two verbs open. The choice is not
free, because §14.1 decided the shape of an object long before anyone had to erase anything
from it: **one project, one environment, one hour**. At any real traffic that is hundreds of
people in one file.

So "remove" is not an option that erases one person. It erases everyone who played in that
hour, and the events it destroys are events those studios are paying to retain (§59).
Nothing about that failure is visible: the object disappears, the manifest says `deleted`,
and the loss surfaces months later as a cold query returning a hole.

Meanwhile the archive is the copy with no second copy. ClickHouse can be rebuilt from it
(§35); it cannot be rebuilt from ClickHouse, whose own retention is the same tier. An
erasure that goes wrong here is not recoverable.

There is also a lower-level obstacle. Parquet is immutable and columnar — there is no
delete-row operation — and `blobstore.Store` had no read method at all, because until this
point nothing read the archive. §34's cold queries, which were expected to be the first
reader, are not built.

## Decision

**An erasure reads the object, drops the rows naming the subject, and writes what remains
back as a new object.** Only an object that held nobody else is deleted outright.

**The replacement is named by a digest of the rows that survive**, in the prefix the
original sat in. This is ADR 0022's rule applied to a second producer of archive objects,
and it buys the same property: running the same redaction twice writes the same bytes to the
same path, so a retry after a crash overwrites rather than accumulates.

**The order is: write the replacement, commit its manifest, then delete the original.**
Appendix C.2's ordering, applied to a rewrite — the window a crash can land in holds two
copies of the surviving events, never none. The original's manifest is still `durable` at
that moment, so the retry simply finds it and redoes the work.

**The replacement inherits `delete_after` unchanged.** A redaction must not extend a
customer's retention, and computing a fresh term from today would make the object outlive
the events inside it (§60).

**The retired manifest is marked `redacted`, a status of its own** rather than `deleted`.
The two are different facts about the same absent object — one reached the end of its
retention, the other was replaced — and §60's sweeper looks only at `durable` and `deleting`,
so a redacted row is never picked up again.

**`blobstore.Store` grows a `Get`.** It arrived with this rather than with §34, and the
package comment says so: an archive that can only be appended to is an archive no erasure
can reach.

**Candidate objects are chosen from the manifests**, by project and by the span the subject
was active in, widened by a margin. The manifest table is the index; nothing lists the
bucket.

**A pass is bounded, and it resumes by manifest id.** Every candidate is downloaded and
decoded, so a subject with a long span takes several passes, and a request that filled its
bound **cannot complete** however long its drain window has been open. The resume point is
the id of the last manifest examined, not a timestamp: a cursor has to advance past the row
it stopped on, and a strict comparison on a timestamp skips anything sharing the last one's
instant while a non-strict one re-selects it and the scan never moves. This was found by
building it the other way — with `created_at >= watermark` the scan livelocks on its own
last row, and a bound of one object never finishes.

## Consequences

- Erasing one person costs a read and a write of every object they appear in. For somebody
  active across a month of hourly objects that is real work, and it is why the archive half
  of a purge is bounded per pass and the request stays in `purging` between passes.
- A rewrite produces a manifest with a higher id than the cursor, so each redacted object is
  read once more on a later pass and found to hold nobody. One extra read per rewrite is what
  a resume point costs, and it is why the bound is five hundred rather than five.
- **The redaction is only as precise as the row predicate.** It matches the actor key or the
  install (§05), which is deliberately the wide reading — an erasure naming an install has
  to reach the rows produced after that install signed in.
- The replacement has a different name, so anything holding a path to an archive object
  holds a path that can stop existing. Nothing does today; §34's cold queries must resolve
  objects through the manifests rather than caching paths.
- A partial redelivery (ADR 0022's residual) produces two overlapping objects, and both are
  redacted independently because both have manifests. The overlap survives the erasure; the
  person does not.
- An object written at an older `schema_version` than the running build is **refused** rather
  than rewritten. Re-encoding from decoded rows would silently drop whatever the two layouts
  do not share, and the rewrite would then be the only surviving copy. There is one schema
  version today, so this has never fired — it exists because the day it would fire is the
  day nobody is thinking about it.
- The margin on the time window is a heuristic, not a proof. It is two days either side of
  what ClickHouse still knows, which covers the gap between a row's `expire_at` and an
  object's `delete_after`; a subject with no trace left in ClickHouse at all falls back to
  the project's whole live archive.

## Alternatives considered

**Delete the whole object.** Erases everyone in that project-hour. Considered only long
enough to write down why not.

**Tombstone the actor and filter on read.** Cheap, and it is not erasure: the data still
exists, and the promise made to the person is that it does not. It is also a promise that
only holds while every reader remembers to apply the filter — §34's cold-query path does not
exist yet, so the first reader would be written against an archive whose contents were not
what they appeared to be.

**Defer to the compaction job**, which §61 mentions and §10.7 owns. Compaction is not built,
and "the erasure runs when the compaction ships" is the kind of dependency that turns a legal
deadline into a roadmap item. When compaction does arrive it should reuse this path rather
than the other way around: compaction merges objects, and a merge that had to also erase
people would be two problems in one job.

**Mark the object and rewrite it lazily on first read.** Moves unbounded work onto a query
path and leaves the data in place until somebody happens to ask for it. An erasure with no
deadline is not one.

**Keep the original path and overwrite in place.** Attractive — nothing holding a path
breaks — but the manifest's `event_count`, `object_size_bytes` and time bounds are immutable
by design (they are what §39 bills and what §104 scopes a breach from), so the row would
have to be mutated into disagreeing with the object it describes. A new object with its own
manifest keeps both truthful.

## Enforcement

- `apps/backend/internal/erasure/archive.go` — `redactArchive`, `rewrite`, `redactedPath`
  and `redactedDigest`; the order of Put, manifest, Delete is the function's shape.
- `apps/backend/internal/archive/row.go` — `Row.Names`, the single definition of which rows
  belong to a subject, shared with the ClickHouse predicate through
  `internal/subject`.
- `apps/backend/ent/schema/regional.go` — the `redacted` status, and the
  unique `(bucket, object_path)` index that makes a repeated rewrite a conflict.
- `TestAnErasureRemovesThePersonAndKeepsEveryoneElse` asserts the bystander's row survives
  in the rewritten object, that the original is gone and marked `redacted`, and that
  `delete_after` did not move.
- `TestAnErasureIsIdempotent` runs the whole purge twice and asserts one live manifest at
  one path — the property the content-derived name exists for.
- `TestABoundedArchiveScanResumesRatherThanSkipping` runs with a bound of one object over two
  objects, asserts the request refuses to complete while the scan is truncated, and asserts it
  converges — which is the test that failed until the cursor moved off the timestamp.
- `TestARedactedObjectIsNeverExpired` asserts §60 does not pick up §61's rows.
