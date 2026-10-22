package transcript

import "strings"

func (source PDFReadingSource) Kind() string {
	if source.SourceKind == "" {
		return "artifact-version"
	}
	return source.SourceKind
}
func (source PDFReadingSource) Key() string { return source.Kind() + "\x00" + source.VersionID }
func (source PDFReadingSource) Valid() bool {
	valid := func(id string) bool { return id != "" && len(id) <= 512 && id == strings.TrimSpace(id) }
	if !valid(source.VersionID) {
		return false
	}
	switch source.Kind() {
	case "artifact-version":
		return valid(source.ArtifactID) && source.ItemID == "" && source.AttachmentID == ""
	case "literature-attachment-version":
		return source.ArtifactID == "" && valid(source.ItemID) && valid(source.AttachmentID)
	default:
		return false
	}
}
func (binding PDFReadingBinding) Source() PDFReadingSource {
	return PDFReadingSource{SourceKind: binding.SourceKind, ArtifactID: binding.ArtifactID, ItemID: binding.ItemID, AttachmentID: binding.AttachmentID, VersionID: binding.VersionID}
}
func (binding PDFReadingBinding) Matches(source PDFReadingSource) bool {
	return binding.Source().Kind() == source.Kind() && binding.ArtifactID == source.ArtifactID && binding.ItemID == source.ItemID && binding.AttachmentID == source.AttachmentID && binding.VersionID == source.VersionID
}
