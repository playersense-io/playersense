/**
 * Type-level assertions, checked by `tsc --noEmit` rather than by a test runner.
 *
 * There is nothing to execute here: every statement is a claim about a type, and the check
 * is whether the file compiles. That is the right shape for this package, because its only
 * product is types and a runtime test would have nothing to run.
 *
 * What these guard is the thin layer in `index.ts`. The generated `schema.d.ts` is the
 * generator's problem; the aliases and the three helpers over it are ours, and each one is
 * a place where a rename in Go turns into a name that quietly resolves to `never` — which
 * is not an error until somebody tries to use it, three files away, with a confusing
 * message.
 */

import type {
  ApiError,
  ParamsOf,
  Project,
  RequestOf,
  ResponseOf,
  Schema
} from './index'

/** Fails to compile unless `T` is exactly `true`. */
type Assert<T extends true> = T
type Equals<A, B> = (<G>() => G extends A ? 1 : 2) extends <G>() => G extends B ? 1 : 2
  ? true
  : false
type IsNever<T> = [T] extends [never] ? true : false

// ---- the aliases name real schemas ---------------------------------------------------
//
// A missing schema resolves to `never`, not to an error, so asserting "not never" is what
// catches a Go rename that this package has not followed. ProjectOut stands for all of
// them: they are generated the same way and fail the same way.

export type _AProjectIsNotNever = Assert<Equals<IsNever<Project>, false>>
export type _AnErrorIsNotNever = Assert<Equals<IsNever<ApiError>, false>>

// A schema that does not exist *does* resolve to never, which is what makes the assertion
// above meaningful rather than vacuous.
// @ts-expect-error — 'NoSuchSchemaOut' is not a schema in the document
export type _AMissingSchemaIsRejected = Schema<'NoSuchSchemaOut'>

// ---- the shape of a project is the shape the API sends --------------------------------
//
// Named fields rather than the whole type: the point is that the generated document
// carries the fields pages read, not that this file lists them all. `cell` and `region`
// were here until ADR 0038 removed them, which is the kind of change this catches.

export type _AProjectHasItsIdentity = Assert<
  Equals<Project['slug'] extends string ? true : false, true>
>
export type _AProjectHasAPolicyTier = Assert<
  Equals<Project['policy_tier'] extends string ? true : false, true>
>

// The generator is configured `immutable: true`, so a page cannot assign to what the API
// sent it. If that option is ever dropped this stops compiling, which is the intent: the
// apps read responses, they do not build them.
export type _AResponseIsReadonly = Assert<
  Equals<Readonly<Project>, Project>
>

// ---- the route helpers resolve --------------------------------------------------------
//
// A path that no longer exists is an error here rather than a silent `never` somewhere in
// a page, which is the whole reason these three helpers are constrained on `keyof paths`.

export type _AListingAnswersWithProjects = Assert<
  Equals<IsNever<ResponseOf<'/v1/organizations/{org}/projects', 'get'>>, false>
>
export type _CreatingTakesABody = Assert<
  Equals<IsNever<RequestOf<'/v1/organizations/{org}/projects', 'post'>>, false>
>
export type _AProjectRouteNamesItsOrganization = Assert<
  Equals<IsNever<ParamsOf<'/v1/organizations/{org}/projects', 'get'>>, false>
>

// A route answering 201 rather than 200 resolves without naming a status, which is the
// reason ResponseOf defaults to whichever success status the route declares.
export type _ACreationAnswersWithoutNamingAStatus = Assert<
  Equals<IsNever<ResponseOf<'/v1/organizations/{org}/projects', 'post'>>, false>
>

// Ingestion answers 202 and nothing else. Without the Extract, this is where a default of
// 200 would have failed.
export type _IngestionAnswers202 = Assert<
  Equals<IsNever<ResponseOf<'/v1/batches', 'post'>>, false>
>

// A path that does not exist is rejected.
// @ts-expect-error — there is no such route
export type _AMissingRouteIsRejected = ResponseOf<'/v1/no-such-route', 'get'>

// And so is a method the route does not serve. This is the case that was silently
// resolving to a non-never type before MethodsOf narrowed the parameter: ingestion is
// POST-only, and asking for its GET should not typecheck.
// @ts-expect-error — /v1/batches serves POST and nothing else
export type _AMissingMethodIsRejected = ResponseOf<'/v1/batches', 'get'>
