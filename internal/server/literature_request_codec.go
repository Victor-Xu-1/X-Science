package server

import (
	"encoding/json"
	"io"
	"net/http"

	workspace "x-science/internal/persistence/workspace"
)

// Only Library import/Patch metadata is an opaque number-preserving object.
// Keep the original envelope limits; PDF and membership decoding is unchanged.
func decodeLiteratureMutationBody(w http.ResponseWriter, r *http.Request, value any) error {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	var trailing any
	if len(r.URL.Query()) != 0 || decoder.Decode(value) != nil || decoder.Decode(&trailing) != io.EOF {
		return workspace.ErrLiteratureInvalid
	}
	return nil
}
