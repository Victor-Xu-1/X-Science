package memoryextract

import (
	"context"
	"errors"
	"testing"

	"x-science/internal/memorytools"
	workspace "x-science/internal/persistence/workspace"
)

func TestWorkspaceExtractionPreparePropagatesCancelledScopeAndCategoryReads(t *testing.T) {
	for _, test := range []struct {
		name      string
		projectID string
		entity    string
		category  string
	}{
		{name: "project-scope-read", projectID: "project"},
		{name: "category-read", entity: "profile", category: "Methods"},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := openExtractionStore(t)
			prepareExtractionScope(t, store)
			createExtractionMemory(t, store, workspace.CreateMemoryInput{
				ID: "mem_unchanged", UserID: "user", Body: "The existing reporting policy remains unchanged.",
				Origin: "extractor", Evidence: "observed", SubjectProjectID: "project", SourceFrameID: "frame",
			})
			if _, err := store.CreateMemoryCategory(t.Context(), "user", "Methods", "Reproducibility methods", true); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			calls := 0
			service := NewService(store, extractionClassifierFunc(func(_ context.Context, texts []string) ([]memorytools.Classification, error) {
				calls++
				// The allowed model result is valid. Only the following real SQLite
				// entity/category read fails, so it cannot become a normal rejection.
				cancel()
				return make([]memorytools.Classification, len(texts)), nil
			}), nil)
			service.newID = func() string {
				t.Error("a failed persistence read produced an accepted append ID")
				return "mem_must_not_be_prepared"
			}
			plan, err := service.Prepare(ctx, ApplyScope{
				UserID: "user", ProjectID: test.projectID, SourceFrameID: "frame",
			}, Operations{Append: []AppendOperation{{
				Text: "Preserve explicit random seeds in reproducible analysis reports.", Evidence: "stated",
				Entity: test.entity, Category: test.category,
			}}})
			if !errors.Is(err, context.Canceled) || calls != 1 {
				t.Fatalf("cancelled persistence read = %#v, classifier calls=%d, err=%v", plan, calls, err)
			}
			if len(plan.Append)+len(plan.Replace)+len(plan.Remove) != 0 ||
				len(plan.Prepared.Appended)+len(plan.Prepared.Replaced)+len(plan.Prepared.Removed) != 0 ||
				len(plan.Prepared.Failures) != 0 || plan.Prepared.PIUnavailable {
				t.Fatalf("a persistence failure became an accepted plan or ordinary rejection: %#v", plan)
			}
			if count, err := store.CountMemoriesForUser(t.Context(), "user"); err != nil || count != 1 {
				t.Fatalf("cancelled preparation changed memory rows: count=%d, err=%v", count, err)
			}
			memory, found, err := store.GetMemoryOwned(t.Context(), "user", "mem_unchanged")
			if err != nil || !found || memory.Body != "The existing reporting policy remains unchanged." {
				t.Fatalf("cancelled preparation changed the existing fact: %#v, found=%v, err=%v", memory, found, err)
			}
		})
	}
}
