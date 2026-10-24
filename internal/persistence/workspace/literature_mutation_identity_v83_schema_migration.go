package workspace

const (
	literatureMutationIdentityV83CallbackID        = "literature-mutation-identity-v83-noop"
	literatureMutationIdentityV83PreflightIdentity = "literature-mutation-identity-v83-journal-only-v1"
	literatureMutationIdentityV83RuleSpec          = "x-science.workspace.literature-mutation-identity.v83"
)

// This journal-only reader floor accompanies Library's v2 request identities.
// It deliberately changes no business schema, legacy identity, or original ACK.
// The ordinary migration transaction is the sole durable marker authority.
var literatureMutationIdentityV83Migration = versionedSchemaMigration{
	version: 83,
	name:    "literature-mutation-identity-reader-floor",
	identityV2: &schemaMigrationIdentityV2{
		CallbackID:        literatureMutationIdentityV83CallbackID,
		PreflightIdentity: literatureMutationIdentityV83PreflightIdentity,
		RuleSpec:          literatureMutationIdentityV83RuleSpec,
	},
}
