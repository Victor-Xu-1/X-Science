package workspace

const DatabaseFilename = "x-science.sqlite"
const DatabaseRelativePath = "workspace/" + DatabaseFilename

// legacyWorkspaceDatabaseFilename is the immutable published v1.1 workspace file
// name. Startup handoff drains and publishes it into the canonical filename;
// Open never discovers sibling legacy files on its own.
const legacyWorkspaceDatabaseFilename = "synonbiomed-v1.1.sqlite"
