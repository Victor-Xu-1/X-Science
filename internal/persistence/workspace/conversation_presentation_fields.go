package workspace

import (
	"encoding/json"
	"errors"
	"math"
	"strings"
	"unicode/utf8"

	transcriptstore "x-science/internal/persistence/transcript"
)

var ErrConversationPresentationStale = errors.New("conversation presentation changed; refresh before editing")
var ErrConversationPresentationInvalid = errors.New("invalid conversation presentation update")

const presentationRevisionKey = "_presentation_revision"

type ConversationPresentationUpdate struct {
	OwnerID, FrameID, IncarnationID string
	ExpectedRevision                *int64
	Extra                           map[string]any
	Name, Description               *string
	changed                         *bool
}

type ConversationPinnedOrderItem struct {
	ID               string  `json:"id"`
	IncarnationID    string  `json:"expected_incarnation_id"`
	ExpectedRevision int64   `json:"expected_presentation_revision"`
	SortOrder        float64 `json:"sortOrder"`
}

// Only display fields are projected. Runtime, model and filesystem authority
// must never be inferred from an arbitrary web_extra object.
func ConversationPresentation(contextData map[string]any) (map[string]any, error) {
	extra, _ := contextData["web_extra"].(map[string]any)
	revision, err := presentationRevision(extra)
	if err != nil {
		return nil, err
	}
	result := map[string]any{"pinned": false, "presentation_revision": revision}
	archivedAt, err := transcriptstore.ConversationArchivedAt(contextData)
	if err != nil {
		return nil, err
	}
	if archivedAt > 0 {
		result["archived_at"] = extra["archived_at"]
	}
	for _, key := range []string{"pinned", "pinned_at", "sortOrder", "selected_view"} {
		if value, ok := extra[key]; ok {
			result[key] = value
		}
	}
	return result, nil
}

func presentationRevision(extra map[string]any) (int64, error) {
	value, ok := extra[presentationRevisionKey]
	if !ok {
		return 0, nil
	}
	n, ok := presentationNumber(value)
	if !ok || n < 0 || n != math.Trunc(n) {
		return 0, ErrConversationPresentationInvalid
	}
	return int64(n), nil
}

func presentationNumber(value any) (float64, bool) {
	var n float64
	switch v := value.(type) {
	case float64:
		n = v
	case int64:
		n = float64(v)
	case int:
		n = float64(v)
	case json.Number:
		var err error
		n, err = v.Float64()
		if err != nil {
			return 0, false
		}
	default:
		return 0, false
	}
	return n, !math.IsNaN(n) && !math.IsInf(n, 0) && math.Abs(n) <= 9007199254740991
}

func ValidateConversationPresentationPatch(extra map[string]any) error {
	if len(extra) == 0 {
		return ErrConversationPresentationInvalid
	}
	for key, value := range extra {
		switch key {
		case "pinned":
			if _, ok := value.(bool); !ok {
				return ErrConversationPresentationInvalid
			}
		case "sortOrder":
			if _, ok := presentationNumber(value); !ok {
				return ErrConversationPresentationInvalid
			}
		case "selected_view":
			text, ok := value.(string)
			if !ok || strings.TrimSpace(text) == "" || utf8.RuneCountInString(text) > 128 {
				return ErrConversationPresentationInvalid
			}
		default:
			return ErrConversationPresentationInvalid
		}
	}
	return nil
}
