package server

import (
	"context"
	"errors"
	"net/http"

	"x-science/internal/literaturediscovery"
)

func (s *Server) handleLiteratureMetadataLookup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		writeWorkspaceJSON(w, http.StatusMethodNotAllowed, map[string]any{"code": "LITERATURE_METHOD_NOT_ALLOWED"})
		return
	}
	var input struct {
		Reference string `json:"reference"`
		Scheme    string `json:"scheme,omitempty"`
	}
	if r.URL.RawQuery != "" || decodeLiteratureMutationBody(w, r, &input) != nil {
		writeWorkspaceJSON(w, http.StatusBadRequest, map[string]any{"code": "LITERATURE_LOOKUP_INVALID"})
		return
	}
	var draft any
	var err error
	switch input.Scheme {
	case "":
		draft, err = s.doiMetadataLookup.Lookup(r.Context(), input.Reference)
	case "pmid":
		draft, err = s.doiMetadataLookup.LookupPMID(r.Context(), input.Reference)
	default:
		err = literaturediscovery.ErrDOIMetadataInvalid
	}
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return
		}
		status, code := http.StatusServiceUnavailable, "LITERATURE_LOOKUP_UNAVAILABLE"
		switch {
		case errors.Is(err, literaturediscovery.ErrDOIMetadataInvalid):
			status, code = http.StatusBadRequest, "LITERATURE_LOOKUP_INVALID"
		case errors.Is(err, literaturediscovery.ErrDOIMetadataNotFound):
			status, code = http.StatusNotFound, "LITERATURE_LOOKUP_NOT_FOUND"
		case errors.Is(err, literaturediscovery.ErrDOIMetadataRateLimited):
			status, code = http.StatusTooManyRequests, "LITERATURE_LOOKUP_RATE_LIMITED"
		case errors.Is(err, literaturediscovery.ErrDOIMetadataTimeout), errors.Is(err, context.DeadlineExceeded):
			status, code = http.StatusGatewayTimeout, "LITERATURE_LOOKUP_TIMEOUT"
		}
		writeWorkspaceJSON(w, status, map[string]any{"code": code})
		return
	}
	writeWorkspaceJSON(w, http.StatusOK, draft)
}
