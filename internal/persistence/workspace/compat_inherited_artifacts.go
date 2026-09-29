package workspace

import transcriptstore "synon-go/internal/persistence/transcript"

const compatibilityInheritedArtifactCTE = transcriptstore.InheritedArtifactScopeCTE

const compatibilityInheritedVersionMatch = `EXISTS (
 SELECT 1 FROM inherited_versions inherited WHERE inherited.artifact_id=a.id AND inherited.version_id=v.id
)`

const compatibilityInheritedHeadMatch = `EXISTS (
 SELECT 1 FROM inherited_heads inherited WHERE inherited.artifact_id=a.id AND inherited.version_number=v.version_number
)`
