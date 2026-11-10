package jobs

import (
	"context"
	"database/sql"
	"errors"
)

var ErrMergePending = errors.New("identity consolidation waits for outstanding sibling transfers")

type rowQuery interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func resolveJobID(ctx context.Context, q rowQuery, id int64) (int64, error) {
	seen := map[int64]bool{}
	for depth := 0; depth < 64; depth++ {
		if seen[id] {
			return 0, errors.New("cyclic representation ownership")
		}
		seen[id] = true
		var owner int64
		e := q.QueryRowContext(ctx, `SELECT canonical FROM linked_jobs WHERE id=?`, id).Scan(&owner)
		if errors.Is(e, sql.ErrNoRows) {
			return id, nil
		}
		if e != nil {
			return 0, e
		}
		id = owner
	}
	return 0, errors.New("representation ownership too deep")
}
func representationOwner(ctx context.Context, q rowQuery, request int64, format string) (int64, error) {
	var id int64
	if e := q.QueryRowContext(ctx, `SELECT id FROM representations WHERE request_id=? AND format=?`, request, format).Scan(&id); e != nil {
		return 0, e
	}
	return resolveJobID(ctx, q, id)
}
func activateCanceled(ctx context.Context, tx *sql.Tx, id int64) error {
	_, e := tx.ExecContext(ctx, `UPDATE representations SET state=CASE WHEN EXISTS(SELECT 1 FROM job_details WHERE id=? AND json_extract(receipt,'$.state')='verified') THEN 'importing' WHEN EXISTS(SELECT 1 FROM transfer_intents WHERE representation_id=? AND state='unresolved') THEN 'retryable_failure' WHEN EXISTS(SELECT 1 FROM transfer_intents WHERE representation_id=? AND state='failed') THEN 'permanent_failure' ELSE 'queued' END,eligible_at=0 WHERE id=? AND state='canceled'`, id, id, id, id)
	return e
}
