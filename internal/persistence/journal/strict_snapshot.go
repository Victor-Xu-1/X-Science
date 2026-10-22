package journal

import (
	"context"
	"errors"
	"strings"
)

// WithStrictSnapshot fences journal replacement, truncation and removal while a
// short external commit validates this snapshot. The callback must not reenter
// this session's journal or perform model/network work. Ordinary ReadAllStrict
// remains the snapshot API when no external side effect needs this fence.
func (j *EventJournal) WithStrictSnapshot(ctx context.Context, sessionID string, commit func([]Entry) error) error {
	if j == nil || strings.TrimSpace(sessionID) == "" || commit == nil {
		return errors.New("strict journal snapshot requires a session and callback")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	lock := j.lockForSession(sessionID)
	lock.Lock()
	defer lock.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	entries, err := j.readAllStrict(sessionID)
	if err != nil {
		return err
	}
	return commit(entries)
}
