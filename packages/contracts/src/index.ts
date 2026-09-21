/**
 * The API's shapes, as TypeScript.
 *
 * Everything here is generated from the application's own OpenAPI document — see
 * `schema.d.ts` and the package README. This file adds nothing to it but names: the
 * generated shape is precise and unpleasant to write out, and a page that wants "what a
 * project looks like" should not have to spell
 * `paths['/v1/organizations/{org}/projects/{project}']['get']['responses'][200]['content']['application/json']`
 * to say so.
 *
 * There is no client here, and that is deliberate. A generated client would decide how
 * auth, retries and error handling work for every caller, and those are decisions the apps
 * should make once, together, in their own fetch wrapper. This package is types only, so it
 * has no runtime and cannot go out of date at runtime.
 */

import type { components, operations, paths } from './schema'

export type { components, operations, paths }

/** Every named schema in the document, keyed by its name. */
export type Schemas = components['schemas']

/** One named schema, by name: `Schema<'ProjectOut'>`. */
export type Schema<Name extends keyof Schemas> = Schemas[Name]

// ---- the shapes pages actually name ------------------------------------------------
//
// Aliased rather than re-exported wholesale, because a name that appears in a page should
// be a name somebody chose. Anything missing is still reachable as `Schema<'Thing'>`,
// which is the escape hatch that keeps this list from having to be exhaustive.

export type Organization = Schema<'OrganizationOut'>
export type Project = Schema<'ProjectOut'>
export type Environment = Schema<'EnvironmentOut'>
export type APIKey = Schema<'APIKeyOut'>
/** Only ever returned once, at creation (§52). */
export type APIKeyCreated = Schema<'APIKeyCreatedOut'>

export type Member = Schema<'MemberOut'>
export type Invitation = Schema<'InvitationOut'>
export type Role = Schema<'RoleOut'>
export type AuditEvent = Schema<'AuditEventOut'>

export type Billing = Schema<'BillingOut'>
export type CreditLot = Schema<'CreditLotOut'>
export type AutoTopUp = Schema<'AutoTopUpOut'>

export type Segment = Schema<'SegmentOut'>
export type FeatureFlag = Schema<'FlagOut'>
export type LiveOpsEvent = Schema<'LiveOpsEventOut'>
export type ConfigVersion = Schema<'ConfigVersionOut'>
export type Draft = Schema<'DraftOut'>

export type CohortQuery = Schema<'CohortQueryIn'>
export type CohortResult = Schema<'CohortResult'>
export type MetricQuery = Schema<'MetricQueryIn'>
export type MetricResult = Schema<'MetricResult'>

export type Erasure = Schema<'ErasureOut'>
export type Export = Schema<'ExportOut'>

/**
 * The error body every refusal carries.
 *
 * Read `code` rather than matching on `message`: the code is the contract and the message
 * is for a person. `details` names the field a validation failure is about.
 */
export type ApiError = Schema<'ErrorResponse'>

// ---- reaching into a route -----------------------------------------------------------
//
// Keyed on the path and the method rather than on an operation id, because the ids are
// **generated from the route**: `GET /v1/organizations/{org}/projects` is
// `get_v1_organizations_by_org_projects`. Those are unpleasant to write, and naming one in
// a page means a route rename breaks the page with a message about a missing operation id
// rather than about a missing route. The path is the thing a person already knows.
//
// `operations` is still exported above for anything that genuinely wants an id.

/**
 * The methods a given path actually serves.
 *
 * The generated `paths` declares every HTTP verb on every route and types the absent ones
 * as `never`, so `keyof paths[Path]` offers `get` on a POST-only route and the helpers
 * below would resolve it to something useless rather than refusing it. This narrows to the
 * verbs that exist, which turns `ResponseOf<'/v1/batches', 'get'>` from a quietly wrong
 * type into a compile error — and makes autocomplete offer only the real ones.
 */
export type MethodsOf<Path extends keyof paths> = {
  [M in Extract<keyof paths[Path], HttpMethod>]-?: paths[Path][M] extends undefined ? never : M
}[Extract<keyof paths[Path], HttpMethod>]

type HttpMethod = 'get' | 'put' | 'post' | 'delete' | 'options' | 'head' | 'patch' | 'trace'

/**
 * The statuses this API answers successfully with.
 *
 * `Status` below defaults to whichever of these a route actually declares, which is not a
 * detail: a route answering only `202` has no `200`, and defaulting to one would make the
 * helper a type error on exactly the routes most worth having it for. 204 is excluded
 * because it carries no body.
 */
type SuccessStatus = 200 | 201 | 202

type Responses<Path extends keyof paths, Method extends MethodsOf<Path>>
  = paths[Path][Method] extends { responses: infer R } ? R : never

/** The JSON body a route answers with: `ResponseOf<'/v1/organizations', 'get'>`. */
export type ResponseOf<
  Path extends keyof paths,
  Method extends MethodsOf<Path>,
  Status extends keyof Responses<Path, Method>
    = Extract<SuccessStatus, keyof Responses<Path, Method>>
> = Responses<Path, Method>[Status] extends {
  content: { 'application/json': infer Body }
}
  ? Body
  : never

/** The JSON body a route accepts: `RequestOf<'/v1/organizations', 'post'>`. */
export type RequestOf<Path extends keyof paths, Method extends MethodsOf<Path>>
  = paths[Path][Method] extends {
    requestBody: { content: { 'application/json': infer Body } }
  }
    ? Body
    : never

/** The path and query parameters a route takes. */
export type ParamsOf<Path extends keyof paths, Method extends MethodsOf<Path>>
  = paths[Path][Method] extends { parameters: infer P } ? P : never
