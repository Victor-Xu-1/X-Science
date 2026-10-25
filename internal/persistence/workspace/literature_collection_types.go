package workspace

import "errors"

var ErrLiteratureCollectionNameConflict = errors.New("literature collection name already exists")

type LiteratureCollection struct {
	ID          string  `json:"id"`
	Revision    int64   `json:"revision"`
	Name        string  `json:"name"`
	Description string  `json:"description"`
	ParentID    *string `json:"parent_id"`
	SortOrder   int64   `json:"sort_order"`
	ItemCount   int64   `json:"item_count"`
}

type LiteratureCollectionPage struct {
	Schema      string                 `json:"schema"`
	Collections []LiteratureCollection `json:"collections"`
}

type LiteratureCollectionMembership struct {
	Schema        string   `json:"schema"`
	ItemID        string   `json:"item_id"`
	CollectionIDs []string `json:"collection_ids"`
}

type LiteratureCollectionReceipt struct {
	Schema       string                `json:"schema"`
	OperationID  string                `json:"operation_id"`
	Action       string                `json:"action"`
	CollectionID string                `json:"collection_id"`
	Collection   *LiteratureCollection `json:"collection,omitempty"`
	ItemID       string                `json:"item_id,omitempty"`
	Included     *bool                 `json:"included,omitempty"`
}

type LiteratureCollectionCreate struct {
	OperationID string `json:"operation_id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	ParentID    string `json:"parent_id,omitempty"`
}

type LiteratureCollectionUpdate struct {
	OperationID      string `json:"operation_id"`
	ExpectedRevision int64  `json:"expected_revision"`
	Name             string `json:"name"`
	Description      string `json:"description,omitempty"`
}
