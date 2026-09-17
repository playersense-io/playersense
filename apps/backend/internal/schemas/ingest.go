package schemas

import (
	"muzak.dev/framework"

	"playersense.dev/backend/internal/protocol"
)

// IngestBatchIn is the batch envelope as a bindable input.
//
// The envelope lives in the protocol package because §9 makes it a contract every SDK
// implements; this type only adds the validation rules, so the wire shape and the rules
// that police it stay in separate files and neither drifts silently.
type IngestBatchIn struct {
	protocol.Batch
}

// Validate bounds the envelope before any of it is trusted.
//
// The rules are deliberately shape-only. Whether the key is good, whether the environment
// matches and whether consent was granted are decisions the handler makes, because each
// needs the resolved tenant and none of them should leak into a 422 that tells an
// unauthenticated caller about the schema.
func (in *IngestBatchIn) Validate(v *muzak.Validation) {
	v.Number(&in.SchemaVersion).Required().Between(1, 1)
	// A UUID, like install_id, because that is what storage requires. Until this rule
	// existed the gateway accepted any eight-character string and answered 202, and the
	// writer then found it unparseable and dropped the whole message with a log line no
	// SDK could see — an acknowledgement that was not true. A contract has to be enforced
	// where it is promised.
	v.String(&in.BatchID).Trim().Required().UUID()
	v.String(&in.InstallID).Trim().Required().UUID()
	v.String(&in.Environment).Trim().Lower().OneOf("development", "staging", "production")

	// The nested members are named with As(). A rule binds to a field by address, and
	// these belong to protocol.SDK and its siblings rather than to this struct, so
	// without a name their failures would report no field at all. Naming them after the
	// JSON path is what makes a 422 point at the member the client actually sent.
	v.String(&in.SDK.Name).As("sdk.name").Trim().Lower().Required().MaxLen(32).
		Matches(`^[a-z0-9._-]+$`).
		Message("must be an SDK name such as unity, unreal or javascript")
	v.String(&in.SDK.Version).As("sdk.version").Trim().Required().MaxLen(32)
	v.String(&in.App.Version).As("app.version").Trim().Required().MaxLen(32)
	v.String(&in.App.Build).As("app.build").Trim().MaxLen(32)

	v.String(&in.Device.Platform).As("device.platform").Trim().Lower().Required().
		OneOf("ios", "android", "windows", "macos", "linux", "web", "console")
	v.String(&in.Device.OSVersion).As("device.os_version").Trim().MaxLen(64)
	v.String(&in.Device.Locale).As("device.locale").Trim().MaxLen(24)

	// §97: the basis has to be stated. A policy version is what makes a later withdrawal
	// scopeable to "everything collected under version N or earlier".
	v.String(&in.Consent.PolicyVersion).As("consent.policy_version").Trim().Required().MaxLen(32)
	v.String(&in.Consent.ATTStatus).As("consent.att_status").Trim().Lower().
		OneOf("authorized", "denied", "not_determined", "restricted")

	// An empty batch is a bug in the SDK's flush condition, not a request worth
	// processing. The upper bound is enforced in the handler, where the per-project
	// limit is known.
	v.Slice(&in.Events).Required().MinItems(1)
}
