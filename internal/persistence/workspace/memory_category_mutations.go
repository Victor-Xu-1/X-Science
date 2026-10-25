package workspace

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// These outcomes describe only category writes. Generic workspace error/status
// mapping and unrelated Memory operations retain their existing contracts.
var (
	ErrMemoryCategoryWriteRejected          = errors.New("memory category write rejected")
	ErrMemoryCategoryOutcomeUnknown         = errors.New("memory category outcome is unknown")
	ErrMemoryCategoryCommittedResultMissing = errors.New("committed memory category result is missing")
)

const memoryCategoryCreateOperation = "memory.category.create"
const memoryCategoryUpdateOperation = "memory.category.update"
const memoryCategoryReceiptReadbackTimeout = 2 * time.Second

type memoryCategoryWriteRejection struct{ cause error }

func (e *memoryCategoryWriteRejection) Error() string { return e.cause.Error() }
func (e *memoryCategoryWriteRejection) Unwrap() error { return e.cause }
func (e *memoryCategoryWriteRejection) Is(target error) bool {
	return target == ErrMemoryCategoryWriteRejected
}
func rejectMemoryCategoryWrite(err error) error { return &memoryCategoryWriteRejection{cause: err} }

type memoryCategoryLedgerResult struct {
	CategoryID string `json:"categoryId"`
}
type memoryCategoryMutation struct{ owner, key, operation, requestHash string }
type memoryCategoryCreateRequest struct {
	Name, Guidance string
	AutoRecall     bool
}
type memoryCategoryUpdateRequest struct {
	CategoryID     string
	Name, Guidance *string
	AutoRecall     *bool
}

func newMemoryCategoryMutation(ctx context.Context, owner, operation string, request any) (memoryCategoryMutation, error) {
	if ctx == nil {
		return memoryCategoryMutation{}, fmt.Errorf("%w: request context is required", ErrMemoryCategoryOutcomeUnknown)
	}
	if err := mutationIdempotencyError(ctx); err != nil {
		return memoryCategoryMutation{}, rejectMemoryCategoryWrite(err)
	}
	owner = strings.TrimSpace(owner)
	if owner == "" {
		return memoryCategoryMutation{}, rejectMemoryCategoryWrite(errors.New("memory user id is required"))
	}
	requestHash, err := mutationRequestHash(request)
	if err != nil {
		return memoryCategoryMutation{}, fmt.Errorf("%w: %w", ErrMemoryCategoryOutcomeUnknown, err)
	}
	// The no-context legacy getter generates an independent UUID. Capture once
	// so transaction insertion and any exact readback use the identical key.
	return memoryCategoryMutation{owner: owner, key: mutationIdempotencyKey(ctx), operation: operation, requestHash: requestHash}, nil
}

func (s *Store) mutateMemoryCategory(ctx context.Context, identity memoryCategoryMutation, apply func(*sql.Tx) (MemoryCategory, error)) (MemoryCategory, error) {
	if s == nil || s.db == nil {
		return MemoryCategory{}, fmt.Errorf("%w: workspace store is closed", ErrMemoryCategoryOutcomeUnknown)
	}
	var category MemoryCategory
	err := s.WithTransaction(ctx, func(tx *sql.Tx) error {
		var result memoryCategoryLedgerResult
		replayed, err := lookupMutationResultTx(ctx, tx, identity.owner, identity.key, identity.operation, identity.requestHash, &result)
		if err != nil {
			return err
		}
		if replayed {
			category, err = loadMemoryCategoryResult(ctx, tx, identity.owner, result.CategoryID)
			return err
		}
		// No mutable row/count/business validation precedes the receipt.
		category, err = apply(tx)
		if err != nil {
			return err
		}
		return insertMutationResultTx(ctx, tx, identity.owner, identity.key, identity.operation, identity.requestHash,
			memoryCategoryLedgerResult{CategoryID: category.ID}, s.now().UTC())
	})
	if err == nil {
		return category, nil
	}
	return s.resolveMemoryCategoryMutationError(ctx, identity, err)
}

func (s *Store) resolveMemoryCategoryMutationError(ctx context.Context, identity memoryCategoryMutation, failure error) (MemoryCategory, error) {
	if errors.Is(failure, ErrMutationIdempotencyConflict) || errors.Is(failure, ErrMemoryCategoryCommittedResultMissing) {
		return MemoryCategory{}, failure
	}
	if ctx.Err() != nil {
		return MemoryCategory{}, fmt.Errorf("%w: %w", ErrMemoryCategoryOutcomeUnknown, errors.Join(failure, ctx.Err(), context.Cause(ctx)))
	}
	// One finite readback under the original request, never a detached retry.
	readCtx, cancel := context.WithTimeout(ctx, memoryCategoryReceiptReadbackTimeout)
	defer cancel()
	var result memoryCategoryLedgerResult
	found, readErr := s.lookupMutationResult(readCtx, identity.owner, identity.key, identity.operation, identity.requestHash, &result)
	if errors.Is(readErr, ErrMutationIdempotencyConflict) {
		return MemoryCategory{}, readErr
	}
	if readErr == nil && found {
		category, err := loadMemoryCategoryResult(readCtx, s.db, identity.owner, result.CategoryID)
		if err == nil || errors.Is(err, ErrMemoryCategoryCommittedResultMissing) {
			return category, err
		}
		readErr = err
	}
	if readErr == nil && !found && errors.Is(failure, ErrMemoryCategoryWriteRejected) {
		return MemoryCategory{}, failure
	}
	// A failed Begin/statement/Commit/readback, including missing receipt after
	// a database error, never proves that this logical key was not committed.
	return MemoryCategory{}, fmt.Errorf("%w: %w", ErrMemoryCategoryOutcomeUnknown, errors.Join(failure, readErr))
}

func loadMemoryCategoryResult(ctx context.Context, queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, owner, categoryID string) (MemoryCategory, error) {
	if categoryID == "" {
		return MemoryCategory{}, errors.New("persisted category result has no identity")
	}
	var category MemoryCategory
	err := queryer.QueryRowContext(ctx, `SELECT c.id,c.user_id,c.name,c.guidance,c.auto_recall,
        (SELECT COUNT(*) FROM memories m WHERE m.category_id=c.id AND m.user_id=c.user_id AND m.superseded_by IS NULL),c.created_at,c.updated_at
        FROM memory_categories c WHERE c.id=? AND c.user_id=?`, categoryID, owner).Scan(
		&category.ID, &category.UserID, &category.Name, &category.Guidance, &category.AutoRecall, &category.RowCount, &category.CreatedAt, &category.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return MemoryCategory{}, ErrMemoryCategoryCommittedResultMissing
	}
	return category, err
}

func ensureMemoryCategoryNameAvailable(ctx context.Context, tx *sql.Tx, owner, name, exceptID string) error {
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM memory_categories WHERE user_id=? AND name_lower=? AND id<>?`, owner, strings.ToLower(name), exceptID).Scan(&count); err != nil {
		return err
	}
	if count != 0 {
		return rejectMemoryCategoryWrite(fmt.Errorf("a category named %q already exists", name))
	}
	return nil
}

func (s *Store) CreateMemoryCategory(ctx context.Context, userID, name, guidance string, autoRecall bool) (MemoryCategory, error) {
	identity, err := newMemoryCategoryMutation(ctx, userID, memoryCategoryCreateOperation, memoryCategoryCreateRequest{Name: name, Guidance: guidance, AutoRecall: autoRecall})
	if err != nil {
		return MemoryCategory{}, err
	}
	return s.mutateMemoryCategory(ctx, identity, func(tx *sql.Tx) (MemoryCategory, error) {
		normalizedName, normalizedGuidance, err := normalizeMemoryCategory(name, guidance)
		if err != nil {
			return MemoryCategory{}, rejectMemoryCategoryWrite(err)
		}
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM memory_categories WHERE user_id=?`, identity.owner).Scan(&count); err != nil {
			return MemoryCategory{}, err
		}
		if count >= maxMemoryCategoriesPerUser {
			return MemoryCategory{}, rejectMemoryCategoryWrite(fmt.Errorf("category limit reached (%d)", maxMemoryCategoriesPerUser))
		}
		if err := ensureMemoryCategoryNameAvailable(ctx, tx, identity.owner, normalizedName, ""); err != nil {
			return MemoryCategory{}, err
		}
		now := s.now().UTC()
		category := MemoryCategory{ID: "memcat_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12], UserID: identity.owner,
			Name: normalizedName, Guidance: normalizedGuidance, AutoRecall: autoRecall, CreatedAt: now, UpdatedAt: now}
		_, err = tx.ExecContext(ctx, `INSERT INTO memory_categories(id,user_id,name,name_lower,guidance,auto_recall,created_at,updated_at)
            VALUES(?,?,?,?,?,?,?,?)`, category.ID, category.UserID, category.Name, strings.ToLower(category.Name), category.Guidance, category.AutoRecall, now, now)
		return category, err
	})
}

func (s *Store) UpdateMemoryCategory(ctx context.Context, userID, categoryID string, input UpdateMemoryCategoryInput) (MemoryCategory, error) {
	// Snapshot pointer values once. Hash original typed presence, never a merge
	// with the current row: later edits cannot change this request's identity.
	if input.Name != nil {
		value := *input.Name
		input.Name = &value
	}
	if input.Guidance != nil {
		value := *input.Guidance
		input.Guidance = &value
	}
	if input.AutoRecall != nil {
		value := *input.AutoRecall
		input.AutoRecall = &value
	}
	request := memoryCategoryUpdateRequest{CategoryID: categoryID, Name: input.Name, Guidance: input.Guidance, AutoRecall: input.AutoRecall}
	identity, err := newMemoryCategoryMutation(ctx, userID, memoryCategoryUpdateOperation, request)
	if err != nil {
		return MemoryCategory{}, err
	}
	return s.mutateMemoryCategory(ctx, identity, func(tx *sql.Tx) (MemoryCategory, error) {
		if input.Name == nil && input.Guidance == nil && input.AutoRecall == nil {
			return MemoryCategory{}, rejectMemoryCategoryWrite(errors.New("at least one category field is required"))
		}
		if strings.TrimSpace(categoryID) == "" {
			return MemoryCategory{}, rejectMemoryCategoryWrite(sql.ErrNoRows)
		}
		current, err := loadMemoryCategoryResult(ctx, tx, identity.owner, strings.TrimSpace(categoryID))
		if errors.Is(err, ErrMemoryCategoryCommittedResultMissing) {
			return MemoryCategory{}, rejectMemoryCategoryWrite(sql.ErrNoRows)
		}
		if err != nil {
			return MemoryCategory{}, err
		}
		name, guidance := current.Name, current.Guidance
		if input.Name != nil {
			name = *input.Name
		}
		if input.Guidance != nil {
			guidance = *input.Guidance
		}
		name, guidance, err = normalizeMemoryCategory(name, guidance)
		if err != nil {
			return MemoryCategory{}, rejectMemoryCategoryWrite(err)
		}
		if err := ensureMemoryCategoryNameAvailable(ctx, tx, identity.owner, name, current.ID); err != nil {
			return MemoryCategory{}, err
		}
		autoRecall := current.AutoRecall
		if input.AutoRecall != nil {
			autoRecall = *input.AutoRecall
		}
		now := s.now().UTC()
		result, err := tx.ExecContext(ctx, `UPDATE memory_categories SET name=?,name_lower=?,guidance=?,auto_recall=?,updated_at=? WHERE id=? AND user_id=?`, name, strings.ToLower(name), guidance, autoRecall, now, current.ID, current.UserID)
		if err != nil {
			return MemoryCategory{}, err
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return MemoryCategory{}, err
		}
		if changed != 1 {
			return MemoryCategory{}, sql.ErrNoRows
		}
		current.Name, current.Guidance, current.AutoRecall, current.UpdatedAt = name, guidance, autoRecall, now
		return current, nil
	})
}
