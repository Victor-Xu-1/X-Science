package workspace

const DatabaseFilename = "x-science.sqlite"
const DatabaseRelativePath = "workspace/" + DatabaseFilename

// legacyWorkspaceDatabaseFilename is the immutable published v1.1 workspace file
// name. Open admits it with a same-directory rename when the canonical file is
// absent; it is never treated as an alternate live authority.
const legacyWorkspaceDatabaseFilename = "synonbiomed-v1.1.sqlite"
