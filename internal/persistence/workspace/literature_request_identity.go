package workspace

import "encoding/json"

// This persisted encoding must ship with its original-journal minimum-reader
// barrier. It is not compatible with a same-schema older Library writer/reader.
type literatureRequestIdentity struct {
	exact       string
	legacyExact string
	legacy      func() (string, error)
}

func (identity literatureRequestIdentity) requestHash() string {
	return "v2:" + identity.exact
}

// The stored row chooses the compatibility rule, never the incoming Go type.
func (identity literatureRequestIdentity) matches(stored string) bool {
	if stored == identity.requestHash() {
		return true
	}
	if !legacyLiteratureRequestHash(stored) {
		// Includes changed v2 requests and unknown/malformed encodings: no aliases.
		return false
	}
	if stored == identity.legacyExact {
		// Exact historical Number("1e400") needs no valid float64 projection.
		return true
	}
	if identity.legacy == nil {
		return false
	}
	legacy, err := identity.legacy()
	return err == nil && stored == legacy
}

func legacyLiteratureRequestHash(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, char := range value {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}

func legacyLiteratureMetadataProjection(raw string, present bool) (map[string]any, error) {
	if !present {
		return nil, nil
	}
	var metadata map[string]any
	// Deliberately the previous metadata-only interface/float64 representation.
	// Failure makes this old-row comparison unavailable, not the request invalid.
	err := json.Unmarshal([]byte(raw), &metadata)
	return metadata, err
}

func literatureImportRequestIdentity(input LiteratureImport, metadata string) (literatureRequestIdentity, error) {
	exact, err := mutationRequestHash(input)
	if err != nil {
		return literatureRequestIdentity{}, err
	}
	present := input.Metadata != nil
	input.Metadata = nil
	return literatureRequestIdentity{exact: exact, legacyExact: exact, legacy: func() (string, error) {
		projected := input
		var err error
		projected.Metadata, err = legacyLiteratureMetadataProjection(metadata, present)
		if err != nil {
			return "", err
		}
		return mutationRequestHash(projected)
	}}, nil
}

func snapshotLiteraturePatchFields(input LiteraturePatch) LiteraturePatch {
	input.MetadataPatch = append(json.RawMessage(nil), input.MetadataPatch...)
	if input.Title != nil {
		title := *input.Title
		input.Title = &title
	}
	if input.Deleted != nil {
		deleted := *input.Deleted
		input.Deleted = &deleted
	}
	return input
}

func literaturePatchRequestIdentity(id string, input LiteraturePatch, metadata string) (literatureRequestIdentity, error) {
	input = snapshotLiteraturePatchFields(input)
	if len(input.MetadataPatch) != 0 {
		return literatureMetadataFieldsRequestIdentity(id, input)
	}
	request := struct {
		ID    string
		Input LiteraturePatch
	}{id, input}
	legacyExact, err := mutationRequestHash(request)
	if err != nil {
		return literatureRequestIdentity{}, err
	}
	present := request.Input.Metadata != nil
	// The original typed digest omits both nil and {} metadata. Only new Patch
	// identities distinguish leave-unchanged from explicit clear; legacy rows
	// retain their original exact/projection hashes and immutable ACKs.
	exact, err := mutationRequestHash(struct {
		TypedRequestHash string `json:"typed_request_hash"`
		MetadataPresent  bool   `json:"metadata_present"`
	}{legacyExact, present})
	if err != nil {
		return literatureRequestIdentity{}, err
	}
	request.Input.Metadata = nil
	return literatureRequestIdentity{exact: exact, legacyExact: legacyExact, legacy: func() (string, error) {
		projected := request
		var err error
		projected.Input.Metadata, err = legacyLiteratureMetadataProjection(metadata, present)
		if err != nil {
			return "", err
		}
		return mutationRequestHash(projected)
	}}, nil
}

// New field deltas have no historical aliases. In particular, matching a
// final object or its old float projection must not borrow another ACK.
func literatureMetadataFieldsRequestIdentity(id string, input LiteraturePatch) (literatureRequestIdentity, error) {
	exact, err := mutationRequestHash(struct {
		Mode             string          `json:"mode"`
		ID               string          `json:"item_id"`
		OperationID      string          `json:"operation_id"`
		ExpectedRevision int64           `json:"expected_revision"`
		Title            *string         `json:"title,omitempty"`
		Patch            json.RawMessage `json:"metadata_patch"`
	}{"metadata-fields.v1", id, input.OperationID, input.ExpectedRevision, input.Title, input.MetadataPatch})
	if err != nil {
		return literatureRequestIdentity{}, err
	}
	return literatureRequestIdentity{
		exact:  exact,
		legacy: func() (string, error) { return "", ErrMutationIdempotencyConflict },
	}, nil
}
