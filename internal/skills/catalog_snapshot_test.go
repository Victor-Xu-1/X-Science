package skills

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func catalogOwnershipSkill() Skill {
	return Skill{Name: "alpha", Description: "alpha workflow", Path: "/skills/alpha/SKILL.md", Body: "alpha instructions",
		NameI18n: map[string]string{"en": "Alpha"}, DescriptionI18n: map[string]string{"en": "Alpha workflow"},
		Tags: []string{"alpha"}, Keywords: []string{"alpha"}, Tools: []string{"tool"}, Arguments: []string{"arg"},
		RequiredCapabilities: []string{"capability"}, ImplementationIdentities: []string{"implementation"},
		RequiredSkills: []string{"dependency"}, CriticalConstraints: []string{"constraint"}, SetupEvidenceURLs: []string{"https://example.test"},
		RequiredEnvironmentPackages: []string{"package"}, PreferredExecutionAssets: []string{"asset"}, References: []string{"reference"}}
}

func mutateCatalogSkillCollections(skill Skill) {
	value := reflect.ValueOf(skill)
	for index := 0; index < value.NumField(); index++ {
		field := value.Field(index)
		switch field.Kind() {
		case reflect.Map:
			for _, key := range field.MapKeys() {
				field.SetMapIndex(key, reflect.ValueOf("changed"))
			}
		case reflect.Slice:
			if field.Len() > 0 {
				field.Index(0).SetString("changed")
			}
		}
	}
}

func TestCatalogRevisionRejectsStaleAndForeignSnapshots(t *testing.T) {
	ctx := context.Background()
	mutations := map[string]func(*Catalog){
		"add":          func(c *Catalog) { c.AddSkill(Skill{Name: "beta"}) },
		"upsert":       func(c *Catalog) { next := catalogOwnershipSkill(); next.Body = "new"; c.UpsertSkill(next) },
		"remove":       func(c *Catalog) { c.RemoveSkill("alpha") },
		"remove-under": func(c *Catalog) { c.RemoveSkillsUnder("/skills") },
		"ABA": func(c *Catalog) {
			next := catalogOwnershipSkill()
			next.Body = "temporary"
			c.UpsertSkill(next)
			c.UpsertSkill(catalogOwnershipSkill())
		},
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			catalog := NewCatalog()
			catalog.AddSkill(catalogOwnershipSkill())
			snapshot := catalog.Snapshot()
			mutate(catalog)
			before := catalog.Skills()
			if changed, err := catalog.CompareApply(ctx, snapshot.Revision, []Skill{{Name: "stale"}}); changed || !errors.Is(err, ErrCatalogRevisionConflict) {
				t.Fatalf("stale apply = %v, %v", changed, err)
			}
			if !reflect.DeepEqual(before, catalog.Skills()) {
				t.Fatal("stale plan partially changed the catalog")
			}
		})
	}
	catalog, other := NewCatalog(), NewCatalog()
	if changed, err := catalog.CompareApply(ctx, other.Snapshot().Revision, nil); changed || !errors.Is(err, ErrCatalogRevisionConflict) {
		t.Fatalf("foreign revision = %v, %v", changed, err)
	}
	if changed, err := catalog.CompareApply(ctx, Revision{}, nil); changed || !errors.Is(err, ErrCatalogRevisionConflict) {
		t.Fatalf("zero revision = %v, %v", changed, err)
	}
	snapshot := catalog.Snapshot()
	catalog.RemoveSkill("absent")
	catalog.RemoveSkillsUnder("/absent")
	catalog.AddLoadError("diagnostic", errors.New("not executable content"))
	if _, err := catalog.CompareApply(ctx, snapshot.Revision, nil); err != nil {
		t.Fatalf("unchanged content advanced revision: %v", err)
	}
}

func TestCatalogOwnershipIncludesSearchAndSnapshots(t *testing.T) {
	writers := map[string]func(*testing.T, *Catalog, Skill){
		"Add":    func(_ *testing.T, c *Catalog, skill Skill) { c.AddSkill(skill) },
		"Upsert": func(_ *testing.T, c *Catalog, skill Skill) { c.UpsertSkill(skill) },
		"CompareApply": func(t *testing.T, c *Catalog, skill Skill) {
			if changed, err := c.CompareApply(context.Background(), c.Snapshot().Revision, []Skill{skill}); !changed || err != nil {
				t.Fatalf("owned input apply = %v, %v", changed, err)
			}
		},
	}
	for name, write := range writers {
		t.Run(name, func(t *testing.T) {
			catalog := NewCatalog()
			skill := catalogOwnershipSkill()
			write(t, catalog, skill)
			mutateCatalogSkillCollections(skill)
			if !reflect.DeepEqual(catalog.Skills(), []Skill{catalogOwnershipSkill()}) {
				t.Fatal("caller changed catalog through its original input")
			}
		})
	}
	readers := map[string]func(*Catalog) Skill{
		"Skills":                 func(c *Catalog) Skill { return c.Skills()[0] },
		"Snapshot":               func(c *Catalog) Skill { return c.Snapshot().Skills[0] },
		"Search":                 func(c *Catalog) Skill { return c.Search("alpha", 1)[0] },
		"SearchRanked":           func(c *Catalog) Skill { return c.SearchRanked("alpha", 1)[0].Skill },
		"SearchActivationRanked": func(c *Catalog) Skill { return c.SearchActivationRanked("alpha", 1)[0].Skill },
	}
	for name, read := range readers {
		t.Run(name, func(t *testing.T) {
			catalog := NewCatalog()
			catalog.AddSkill(catalogOwnershipSkill())
			snapshot := catalog.Snapshot()
			mutateCatalogSkillCollections(read(catalog))
			if !reflect.DeepEqual(catalog.Skills(), []Skill{catalogOwnershipSkill()}) {
				t.Fatal("caller changed catalog through a read result")
			}
			if changed, err := catalog.CompareApply(context.Background(), snapshot.Revision, nil); changed || err != nil {
				t.Fatalf("owned read changed catalog revision = %v, %v", changed, err)
			}
		})
	}
	nilAndEmpty := Skill{Name: "empty", Tags: []string{}, NameI18n: map[string]string{}, References: []string{}}
	catalog := NewCatalog()
	catalog.UpsertSkill(nilAndEmpty)
	if !reflect.DeepEqual(catalog.Snapshot().Skills[0], nilAndEmpty) {
		t.Fatal("clone changed nil and empty collection semantics")
	}
}

func TestCatalogCompareApplyCancellationAndBatch(t *testing.T) {
	catalog := NewCatalog()
	snapshot := catalog.Snapshot()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if changed, err := catalog.CompareApply(ctx, snapshot.Revision, []Skill{{Name: "canceled"}}); changed || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled apply = %v, %v", changed, err)
	}
	if changed, err := catalog.CompareApply(nil, snapshot.Revision, []Skill{{Name: "nil-context"}}); changed || err == nil {
		t.Fatalf("nil context = %v, %v", changed, err)
	}
	ctx = context.Background()
	upserts := []Skill{catalogOwnershipSkill(), {Name: "beta", Body: "second complete source"}}
	if changed, err := catalog.CompareApply(ctx, snapshot.Revision, upserts); !changed || err != nil || len(catalog.Skills()) != 2 {
		t.Fatalf("complete batch = %v, %v", changed, err)
	}
	snapshot = catalog.Snapshot()
	catalog.UpsertSkill(catalogOwnershipSkill())
	if changed, err := catalog.CompareApply(ctx, snapshot.Revision, upserts); changed || err != nil {
		t.Fatalf("equal upsert/batch advanced revision = %v, %v", changed, err)
	}
	if _, err := catalog.CompareApply(ctx, snapshot.Revision, nil); err != nil {
		t.Fatalf("equal batch advanced revision: %v", err)
	}
}

func TestCatalogCompareApplyNetNoopKeepsRevision(t *testing.T) {
	catalog := NewCatalog()
	original := catalogOwnershipSkill()
	catalog.UpsertSkill(original)
	snapshot := catalog.Snapshot()
	intermediate := original
	intermediate.Category = "intermediate-presentation"
	changed, err := catalog.CompareApply(context.Background(), snapshot.Revision, []Skill{intermediate, original})
	if changed || err != nil {
		t.Errorf("net-equal batch reported a content change: %v, %v", changed, err)
	}
	if !reflect.DeepEqual(catalog.Skills(), snapshot.Skills) {
		t.Fatal("net-equal batch changed the final catalog")
	}
	if _, err := catalog.CompareApply(context.Background(), snapshot.Revision, nil); err != nil {
		t.Errorf("net-equal batch advanced revision: %v", err)
	}
}
