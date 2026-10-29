package workspace

type LiteratureLifecycle string

const (
	LiteratureLifecycleActive  LiteratureLifecycle = "active"
	LiteratureLifecycleDeleted LiteratureLifecycle = "deleted"
)

// Both paged and complete-ID reads share these filters. Owner always comes
// separately from the authenticated caller, never from a request-body field.
type LiteratureQueryFilters struct {
	Query, ProjectID, CollectionID string
	ReadingStatus                  LiteratureReadingStatus
	Lifecycle                      LiteratureLifecycle
}

func literatureScope(owner string, filters LiteratureQueryFilters) literatureQueryScope {
	return literatureQueryScope{owner: owner, query: filters.Query, project: filters.ProjectID, collection: filters.CollectionID, readingStatus: filters.ReadingStatus, lifecycle: filters.Lifecycle}
}
