package skills

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"slices"
	"strings"
)

var ErrCatalogRevisionConflict = errors.New("skill catalog changed after its snapshot")

// Nonzero-sized identities cannot wrap or reuse an identity retained by a
// snapshot. The owner also prevents a token from a different Catalog matching.
type catalogRevision struct{ marker byte }

type Revision struct {
	owner    *Catalog
	identity *catalogRevision
}

type CatalogSnapshot struct {
	Revision Revision
	Skills   []Skill
}

func (c *Catalog) Snapshot() CatalogSnapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return CatalogSnapshot{Revision: Revision{owner: c, identity: c.revision}, Skills: cloneSkills(c.skills)}
}

// CompareApply publishes a caller-owned upsert plan against one exact snapshot.
// It performs no IO, callbacks or retries, and never partially applies a stale
// plan. Source authorization is separate from this content revision.
func (c *Catalog) CompareApply(ctx context.Context, expected Revision, upserts []Skill) (bool, error) {
	if ctx == nil {
		return false, errors.New("skill catalog apply context is required")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if expected.owner != c || expected.identity != c.revision {
		return false, ErrCatalogRevisionConflict
	}
	// Mutations replace owned Skill values rather than modifying their nested
	// collections. A shallow before-slice therefore preserves the complete
	// prior content while a batch may temporarily replace and restore an entry.
	before := slices.Clone(c.skills)
	for _, skill := range upserts {
		c.upsertSkillLocked(skill)
	}
	changed := !reflect.DeepEqual(before, c.skills)
	if changed {
		c.advanceRevisionLocked()
	}
	return changed, nil
}

func (c *Catalog) advanceRevisionLocked() {
	c.revision = &catalogRevision{}
}

func (c *Catalog) upsertSkillLocked(skill Skill) bool {
	for index := range c.skills {
		if !strings.EqualFold(c.skills[index].Name, skill.Name) {
			continue
		}
		if reflect.DeepEqual(c.skills[index], skill) {
			return false
		}
		c.skills[index] = cloneSkill(skill)
		c.sortSkillsLocked()
		return true
	}
	c.skills = append(c.skills, cloneSkill(skill))
	c.sortSkillsLocked()
	return true
}

func cloneSkills(values []Skill) []Skill {
	out := make([]Skill, len(values))
	for index, skill := range values {
		out[index] = cloneSkill(skill)
	}
	return out
}

func cloneSkill(skill Skill) Skill {
	skill.NameI18n = maps.Clone(skill.NameI18n)
	skill.DescriptionI18n = maps.Clone(skill.DescriptionI18n)
	skill.Tags = slices.Clone(skill.Tags)
	skill.Keywords = slices.Clone(skill.Keywords)
	skill.Tools = slices.Clone(skill.Tools)
	skill.Arguments = slices.Clone(skill.Arguments)
	skill.RequiredCapabilities = slices.Clone(skill.RequiredCapabilities)
	skill.ImplementationIdentities = slices.Clone(skill.ImplementationIdentities)
	skill.RequiredSkills = slices.Clone(skill.RequiredSkills)
	skill.CriticalConstraints = slices.Clone(skill.CriticalConstraints)
	skill.SetupEvidenceURLs = slices.Clone(skill.SetupEvidenceURLs)
	skill.RequiredEnvironmentPackages = slices.Clone(skill.RequiredEnvironmentPackages)
	skill.PreferredExecutionAssets = slices.Clone(skill.PreferredExecutionAssets)
	skill.References = slices.Clone(skill.References)
	return skill
}
