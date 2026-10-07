package transfer

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// A private disk-backed index gives exact duplicate rejection without an
// inventory-sized map. The transaction is disposable and never a user receipt.
type PathIndex struct {
	directory string
	path      string
	database  *sql.DB
	tx        *sql.Tx
	insert    *sql.Stmt
}

func NewPathIndex(ctx context.Context, parent string) (_ *PathIndex, resultErr error) {
	if !filepath.IsAbs(parent) {
		return nil, errors.New("output inventory index requires an owned absolute staging directory")
	}
	directory, err := os.MkdirTemp(parent, ".output-index-")
	if err != nil {
		return nil, err
	}
	index := &PathIndex{directory: directory, path: filepath.Join(directory, "index.sqlite")}
	defer func() {
		if resultErr != nil {
			resultErr = errors.Join(resultErr, index.Close())
		}
	}()
	file, err := os.OpenFile(index.path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := file.Close(); err != nil {
		return nil, err
	}
	index.database, err = sql.Open("sqlite", index.path)
	if err != nil {
		return nil, err
	}
	index.database.SetMaxOpenConns(1)
	if _, err := index.database.ExecContext(ctx, "PRAGMA journal_mode=OFF; PRAGMA synchronous=OFF; PRAGMA cache_size=-2048; CREATE TABLE paths (path TEXT PRIMARY KEY) WITHOUT ROWID;"); err != nil {
		return nil, err
	}
	index.tx, err = index.database.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	index.insert, err = index.tx.PrepareContext(ctx, "INSERT INTO paths(path) VALUES (?)")
	if err != nil {
		return nil, err
	}
	return index, nil
}

func (index *PathIndex) Add(ctx context.Context, name string) error {
	_, err := index.insert.ExecContext(ctx, name)
	if err == nil {
		return nil
	}
	var coded interface{ Code() int }
	if errors.As(err, &coded) && coded.Code()&0xff == 19 {
		return errors.New("output inventory contains a duplicate path")
	}
	return err
}

func (index *PathIndex) Contains(ctx context.Context, name string) (bool, error) {
	var exists int
	err := index.tx.QueryRowContext(ctx, "SELECT 1 FROM paths WHERE path=?", name).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func (index *PathIndex) Close() error {
	var result error
	if index.insert != nil {
		result = errors.Join(result, index.insert.Close())
	}
	if index.tx != nil {
		if err := index.tx.Rollback(); !errors.Is(err, sql.ErrTxDone) {
			result = errors.Join(result, err)
		}
	}
	if index.database != nil {
		result = errors.Join(result, index.database.Close())
	}
	if err := os.Remove(index.path); !errors.Is(err, os.ErrNotExist) {
		result = errors.Join(result, err)
	}
	return errors.Join(result, os.Remove(index.directory))
}
