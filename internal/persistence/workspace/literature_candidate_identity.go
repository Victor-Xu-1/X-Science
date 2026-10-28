package workspace

import (
	"context"
	"database/sql"
	"strings"
	"unicode"
	"x-science/internal/literaturediscovery"
)

// Candidate identities reuse the provider-query authority. Multiple distinct
// values for one scheme are ambiguous and cannot silently choose an item.
func candidateIdentities(metadata map[string]any) (map[string]string, error) {
	return candidateIdentitiesFrom(metadata, true)
}

func candidateIdentitiesFrom(metadata map[string]any, strict bool) (map[string]string, error) {
	out := map[string]string{}
	add := func(scheme, value string) error {
		if strings.TrimSpace(value) == "" {
			return nil
		}
		id, ok := literaturediscovery.NormalizeIdentifier(literaturediscovery.Identifier{Scheme: scheme, Value: value})
		if !ok {
			if strict {
				return ErrLiteratureInvalid
			}
			return nil
		}
		if old := out[id.Scheme]; old != "" && old != id.Value {
			return ErrLiteratureConflict
		}
		out[id.Scheme] = id.Value
		return nil
	}
	for _, scheme := range []string{"doi", "pmid", "pmcid", "arxiv"} {
		if v, ok := metadata[scheme].(string); ok {
			if err := add(scheme, v); err != nil {
				return nil, err
			}
		}
	}
	if rows, ok := metadata["identifiers"].([]any); ok {
		for _, raw := range rows {
			row, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			scheme, _ := row["scheme"].(string)
			value, _ := row["value"].(string)
			switch strings.ToLower(strings.TrimSpace(scheme)) {
			case "doi", "pmid", "pmcid", "arxiv":
				if err := add(scheme, value); err != nil {
					return nil, err
				}
			}
		}
	}
	return out, nil
}
func candidateIdentityMatch(a, b map[string]string) (bool, error) {
	match, conflict := false, false
	for k, v := range a {
		if w := b[k]; w != "" {
			if v == w {
				match = true
			} else {
				conflict = true
			}
		}
	}
	if match && conflict {
		return false, ErrLiteratureConflict
	}
	return match, nil
}
func candidateIdentityConflict(a, b map[string]string) bool {
	for k, v := range a {
		if w := b[k]; w != "" && v != w {
			return true
		}
	}
	return false
}
func candidateString(value string, max int) bool {
	if value == "" || len(value) > max || strings.TrimSpace(value) != value {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// Historical Library metadata can contain multiple identities in a single
// scheme. Keep them isolated until a candidate actually matches that item.
func legacyCandidateIdentitySets(metadata map[string]any) map[string]map[string]struct{} {
	out := map[string]map[string]struct{}{}
	add := func(scheme, value string) {
		id, ok := literaturediscovery.NormalizeIdentifier(literaturediscovery.Identifier{Scheme: scheme, Value: value})
		if !ok {
			return
		}
		if out[id.Scheme] == nil {
			out[id.Scheme] = map[string]struct{}{}
		}
		out[id.Scheme][id.Value] = struct{}{}
	}
	for _, scheme := range []string{"doi", "pmid", "pmcid", "arxiv"} {
		if value, ok := metadata[scheme].(string); ok {
			add(scheme, value)
		}
	}
	if rows, ok := metadata["identifiers"].([]any); ok {
		for _, raw := range rows {
			if row, ok := raw.(map[string]any); ok {
				scheme, _ := row["scheme"].(string)
				value, _ := row["value"].(string)
				add(scheme, value)
			}
		}
	}
	return out
}
func legacyCandidateIdentityMatch(ids map[string]string, sets map[string]map[string]struct{}, sourceMatch bool) (bool, error) {
	matched, conflict := sourceMatch, false
	for _, values := range sets {
		if len(values) > 1 {
			conflict = true
		}
	}
	for scheme, value := range ids {
		if values := sets[scheme]; len(values) > 0 {
			if _, ok := values[value]; ok {
				matched = true
			} else {
				conflict = true
			}
		}
	}
	if matched && conflict {
		return false, ErrLiteratureConflict
	}
	return matched, nil
}

// find matching stored items without trusting a single identifier when other
// shared identifiers conflict; ambiguous existing matches never auto-merge.
func findMatchingLibraryItem(ctx context.Context, tx *sql.Tx, owner string, ids map[string]string, provider, external string) (string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id,metadata_json FROM literature_items WHERE owner_user_id=? AND deleted_at IS NULL`, owner)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	matchID := ""
	for rows.Next() {
		var id, raw string
		if err = rows.Scan(&id, &raw); err != nil {
			return "", err
		}
		meta, e := decodeLiteratureMetadataObject(raw)
		if e != nil {
			return "", e
		}
		sourceMatch := false
		if provider == "semantic-scholar" {
			if value, _ := meta["semantic_scholar_id"].(string); value != "" && strings.EqualFold(value, external) {
				sourceMatch = true
			}
		}
		same, e := legacyCandidateIdentityMatch(ids, legacyCandidateIdentitySets(meta), sourceMatch)
		if e != nil {
			return "", e
		}
		if same {
			if matchID != "" && matchID != id {
				return "", ErrLiteratureConflict
			}
			matchID = id
		}
	}
	if err = rows.Err(); err != nil {
		return "", err
	}
	return matchID, nil
}
