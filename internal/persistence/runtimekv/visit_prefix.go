package runtimekv

import "context"

// VisitPrefix streams detached entries without loading unrelated sessions or
// an unbounded audit history into memory. The visitor must not reenter Store.
func (s *Store) VisitPrefix(ctx context.Context, namespace, prefix string, visit func(Entry) error) error {
	if err := validateNamespace(namespace); err != nil {
		return err
	}
	if err := validateKey(prefix); err != nil {
		return err
	}
	if err := s.lockContext(ctx); err != nil {
		return err
	}
	defer s.mu.Unlock()
	if err := s.ensureDatabase(); err != nil {
		return err
	}
	// Callers use an ASCII key prefix; the parameterized range uses the primary
	// namespace/key index and does not interpret '%' or '_' as wildcards.
	rows, err := s.db.QueryContext(ctx, `SELECT namespace,entry_key,value_json,version,updated_at
 FROM runtime_state_entries WHERE namespace=? AND entry_key>=? AND entry_key<? ORDER BY entry_key`, namespace, prefix, prefix+"\U0010ffff")
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		entry, err := scanEntry(rows)
		if err != nil {
			return err
		}
		if err := visit(entry); err != nil {
			return err
		}
	}
	return rows.Err()
}
