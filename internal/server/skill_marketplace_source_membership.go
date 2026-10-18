package server

import (
	"errors"
	"sort"
	"strings"
)

type webSkillMarketplaceSkillRevision struct {
	SHA        string  `json:"sha"`
	License    *string `json:"license"`
	LicenseRef string  `json:"license_ref,omitempty"`
	ImportedAt string  `json:"imported_at"`
}

// Incremental imports preserve the provenance of bytes already installed from
// this repository. A repository-level SHA is truthful only for a single pin;
// the import response still describes its own newly imported commit.
func mergeSkillMarketplaceSource(existing, incoming webSkillMarketplaceSource) (webSkillMarketplaceSource, error) {
	if existing.Slug != incoming.Slug || !strings.EqualFold(existing.Repo, incoming.Repo) {
		return webSkillMarketplaceSource{}, errors.New("marketplace source slug belongs to a different repository")
	}
	merged := incoming
	if len(existing.Skills) > 0 {
		merged.Removable = existing.Removable && incoming.Removable
	}
	merged.Skills = nil
	merged.Hashes = make(map[string]string)
	merged.SkillRevisions = make(map[string]webSkillMarketplaceSkillRevision)
	names := make(map[string]string)
	for _, source := range []webSkillMarketplaceSource{existing, incoming} {
		seen := make(map[string]bool)
		for _, name := range source.Skills {
			key := strings.ToLower(name)
			if key == "" || seen[key] || (names[key] != "" && names[key] != name) {
				return webSkillMarketplaceSource{}, errors.New("marketplace source contains ambiguous Skill membership")
			}
			seen[key], names[key] = true, name
			revision, known := source.SkillRevisions[name]
			if !known {
				// Legacy single-pin records can describe only their tracked
				// members, never an orphan absent from their membership list.
				revision = webSkillMarketplaceSkillRevision{SHA: source.SHA, License: source.License, ImportedAt: source.ImportedAt}
			}
			merged.Hashes[name] = source.Hashes[name]
			merged.SkillRevisions[name] = revision
		}
	}
	for _, name := range names {
		merged.Skills = append(merged.Skills, name)
	}
	sort.Strings(merged.Skills)
	deriveSkillMarketplaceSourcePin(&merged)
	return merged, nil
}

func deriveSkillMarketplaceSourcePin(source *webSkillMarketplaceSource) {
	source.SHA, source.License = "", nil
	for index, name := range source.Skills {
		revision := source.SkillRevisions[name]
		if !githubCommitPattern.MatchString(revision.SHA) {
			source.SHA, source.License = "", nil
			return
		}
		if index == 0 {
			source.SHA = revision.SHA
			if strings.EqualFold(revision.LicenseRef, revision.SHA) {
				source.License = revision.License
			}
			continue
		}
		if source.SHA != revision.SHA {
			source.SHA, source.License = "", nil
			return
		}
		if source.License == nil || revision.License == nil || !strings.EqualFold(revision.LicenseRef, revision.SHA) || *source.License != *revision.License {
			source.License = nil
		}
	}
}
