// Package dataclass carries the §96 data classification.
//
// Every column in both databases is annotated with one of these, and
// `migrate verify` reads them back out of pg_description and
// system.columns.comment and fails on any column that has none. The
// classification is the single source the Article 30 record, the Play Data Safety
// declaration and the Apple privacy manifest are generated from — maintained by hand,
// those three drift apart within a release.
package dataclass

const (
	Identifier  = "class=identifier"  // identifies a person or device
	Behavioural = "class=behavioural" // what a person did
	Derived     = "class=derived"     // computed from behaviour, still attributable
	Financial   = "class=financial"   // revenue, purchases, entitlements
	None        = "class=none"        // organization, project, infrastructure
)

// Valid reports whether a comment carries a classification this document defines.
func Valid(class string) bool {
	switch class {
	case "identifier", "behavioural", "derived", "financial", "none":
		return true
	}
	return false
}
