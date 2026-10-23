package server

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"x-science/internal/literaturediscovery"
	workspace "x-science/internal/persistence/workspace"
)

type libraryRelatedCatalog struct{ store *workspace.Store }

func (catalog libraryRelatedCatalog) GetReference(ctx context.Context, owner, id string) (literaturediscovery.Reference, error) {
	item, err := catalog.store.GetLiteratureItem(ctx, owner, id)
	if errors.Is(err, workspace.ErrLiteratureUnavailable) || (err == nil && item.Deleted) {
		return literaturediscovery.Reference{}, literaturediscovery.ErrReferenceUnavailable
	}
	if err != nil {
		return literaturediscovery.Reference{}, err
	}
	reference := literaturediscovery.Reference{ID: item.ID, Revision: item.MetadataRevision, Title: item.Title}
	appendIdentifier := func(scheme, value string) {
		if normalized, ok := literaturediscovery.NormalizeIdentifier(literaturediscovery.Identifier{Scheme: scheme, Value: value}); ok {
			reference.Identifiers = append(reference.Identifiers, normalized)
		}
	}
	// Existing metadata is user-editable. Normalize only supported bibliographic
	// identifiers; no field becomes a provider URL, permission or accepted PDF.
	if identifiers, ok := item.Metadata["identifiers"].([]any); ok {
		for _, raw := range identifiers {
			if row, ok := raw.(map[string]any); ok {
				scheme, _ := row["scheme"].(string)
				value, _ := row["value"].(string)
				appendIdentifier(scheme, value)
			}
		}
	}
	for _, scheme := range []string{"doi", "pmid", "pmcid", "arxiv"} {
		if value, ok := item.Metadata[scheme].(string); ok {
			appendIdentifier(scheme, value)
		}
	}
	reference.SemanticScholarID, _ = item.Metadata["semantic_scholar_id"].(string)
	return reference, nil
}

func (s *Server) relatedLiteratureFinder() *literaturediscovery.Finder {
	s.literatureRelatedOnce.Do(func() {
		s.literatureRelated = literaturediscovery.New(literaturediscovery.Options{
			Catalog: libraryRelatedCatalog{store: s.workspaceStore},
			ClientForURL: func(ctx context.Context, endpoint string) (*http.Client, error) {
				return s.publicSourceHTTPClient(ctx, endpoint, s.httpClient)
			},
			APIKey: s.relatedLiteratureAPIKey,
		})
	})
	return s.literatureRelated
}

func (s *Server) relatedLiteratureAPIKey(ctx context.Context, owner string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if s.secretStore == nil {
		return "", nil
	}
	secrets, err := s.secretStore.ListForUser(owner)
	if err != nil {
		return "", err
	}
	var value string
	for _, secret := range secrets {
		if secret.Provider != "literature" {
			continue
		}
		key, _ := secret.CredentialObject()["semantic_scholar_api_key"].(string)
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if len(key) > 16384 || strings.ContainsAny(key, "\r\n") || (value != "" && value != key) {
			return "", errors.New("literature credential unavailable")
		}
		value = key
	}
	return value, ctx.Err()
}

func (s *Server) handleLiteratureRelated(w http.ResponseWriter, r *http.Request, owner string) {
	var input literaturediscovery.Request
	if len(r.URL.Query()) != 0 || decodePDFReadingBody(w, r, &input) != nil {
		writeLiteratureError(w, workspace.ErrLiteratureInvalid)
		return
	}
	result, err := s.relatedLiteratureFinder().Run(r.Context(), owner, input)
	if errors.Is(err, literaturediscovery.ErrInvalid) {
		writeLiteratureError(w, workspace.ErrLiteratureInvalid)
		return
	}
	if err != nil {
		if r.Context().Err() == nil {
			writeLiteratureError(w, err)
		}
		return
	}
	writeWorkspaceJSON(w, http.StatusOK, result)
}
