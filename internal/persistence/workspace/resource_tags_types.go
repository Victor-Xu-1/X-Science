package workspace

import "errors"

const (
	ResourceFavoriteTagID           = "tag-favorite"
	ResourceTagTimestampMax   int64 = 8_640_000_000_000_000
	resourceTagSnapshotSchema       = "x-science.resource-tags.v1"
	resourceTagReceiptSchema        = "x-science.resource-tag-receipt.v1"
)

var (
	ErrResourceTagInvalid      = errors.New("invalid resource tag request")
	ErrResourceTagUnavailable  = errors.New("resource tag or target unavailable")
	ErrResourceTagConflict     = errors.New("resource tag changed")
	ErrResourceTagNameConflict = errors.New("resource tag name already exists")
	ErrResourceTagProtected    = errors.New("system resource tag is protected")
)

// The catalog belongs to an authenticated account. Bibliographic keywords and
// item metadata are independent; assignments refer to a canonical resource ID.
type ResourceTag struct {
	ID        string `json:"id"`
	SystemKey string `json:"system_key,omitempty"`
	Name      string `json:"name,omitempty"`
	IconKey   string `json:"icon_key,omitempty"`
	ColorKey  string `json:"color_key,omitempty"`
	CreatedAt int64  `json:"created_at"`
	UpdatedAt int64  `json:"updated_at"`
}

type ResourceTagAssignment struct {
	TagID        string `json:"tag_id"`
	ResourceType string `json:"resource_type"`
	ResourceID   string `json:"resource_id"`
	CreatedAt    int64  `json:"created_at"`
}

type ResourceTagSnapshot struct {
	Schema        string                  `json:"schema"`
	Revision      int64                   `json:"revision"`
	ResourceTypes []string                `json:"resource_types"`
	Tags          []ResourceTag           `json:"tags"`
	Assignments   []ResourceTagAssignment `json:"assignments"`
}

type ResourceTagReceipt struct {
	Schema      string              `json:"schema"`
	OperationID string              `json:"operation_id"`
	Action      string              `json:"action"`
	TagID       string              `json:"tag_id,omitempty"`
	Snapshot    ResourceTagSnapshot `json:"snapshot"`
}

type ResourceTagCreate struct {
	OperationID string `json:"operation_id"`
	Name        string `json:"name"`
	IconKey     string `json:"icon_key"`
	ColorKey    string `json:"color_key"`
}

type ResourceTagUpdate struct {
	OperationID       string `json:"operation_id"`
	ExpectedUpdatedAt int64  `json:"expected_updated_at"`
	Name              string `json:"name"`
	IconKey           string `json:"icon_key"`
	ColorKey          string `json:"color_key"`
}

type ResourceTagOrder struct {
	OperationID string   `json:"operation_id"`
	TagIDs      []string `json:"tag_ids"`
}

type ResourceTagSetAssignment struct {
	OperationID  string `json:"operation_id"`
	TagID        string `json:"tag_id"`
	ResourceType string `json:"resource_type"`
	ResourceID   string `json:"resource_id"`
	Assigned     bool   `json:"assigned"`
}
