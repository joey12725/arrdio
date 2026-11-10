package jobs

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

func (s *Store) canonicalID(ctx context.Context, id int64) int64 {
	var canonical int64
	if s.db.QueryRowContext(ctx, `SELECT canonical FROM merged_requests WHERE id=?`, id).Scan(&canonical) == nil {
		return canonical
	}
	return id
}

// Canonicalize joins text/photo/catalog aliases before any source effect. Existing
// format owners keep their receipts; redundant jobs follow that owner instead of
// spending again. Old request IDs continue to resolve through merged_requests.
func (s *Store) Canonicalize(ctx context.Context, l Lease, in Input, c Candidate) (bool, error) {
	l.RequestID = s.canonicalID(ctx, l.RequestID)
	resolved := in
	resolved.Title = c.Title
	if c.Author != "" {
		resolved.Author = c.Author
	}
	aliases := []string{identity(resolved), "source:" + c.Provider + ":" + c.ID + ":" + l.Format + ":" + normalized(in.Volume) + in.Constraints.scope()}
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return false, e
	}
	defer tx.Rollback()
	var valid int
	if e = tx.QueryRowContext(ctx, `SELECT 1 FROM representations WHERE id=? AND lease_token=? AND state='matching' AND lease_until>?`, l.ID, l.Token, time.Now().Unix()).Scan(&valid); e != nil {
		return false, e
	}
	canonical := l.RequestID
	for _, alias := range aliases {
		var id int64
		e := tx.QueryRowContext(ctx, `SELECT request_id FROM work_aliases WHERE alias=?`, alias).Scan(&id)
		if e == nil && id != l.RequestID {
			canonical = id
			break
		}
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return false, e
		}
	}
	if canonical == l.RequestID {
		for _, a := range aliases {
			if _, e = tx.ExecContext(ctx, `INSERT OR IGNORE INTO work_aliases VALUES(?,?)`, a, canonical); e != nil {
				return false, e
			}
		}
		return true, tx.Commit()
	}
	rows, e := tx.QueryContext(ctx, `SELECT id,format,state FROM representations WHERE request_id=?`, l.RequestID)
	if e != nil {
		return false, e
	}
	type child struct {
		id            int64
		format, state string
	}
	children := []child{}
	for rows.Next() {
		var c child
		if e = rows.Scan(&c.id, &c.format, &c.state); e != nil {
			rows.Close()
			return false, e
		}
		children = append(children, c)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return false, e
	}
	keepCurrent := true
	// Do not consolidate a parent while corresponding children both own effects.
	// Linking either outstanding receipt would remove it from recovery dispatch.
	for _, child := range children {
		actual, e := resolveJobID(ctx, tx, child.id)
		if e != nil {
			return false, e
		}
		other, e := representationOwner(ctx, tx, canonical, child.format)
		if errors.Is(e, sql.ErrNoRows) {
			continue
		}
		if e != nil {
			return false, e
		}
		if actual == other {
			continue
		}
		var outstanding, effects int
		if e = tx.QueryRowContext(ctx, `SELECT COALESCE(sum(CASE WHEN state='unresolved' THEN 1 ELSE 0 END),0),count(*) FROM transfer_intents WHERE representation_id IN (?,?) AND state NOT IN ('rejected','failed')`, actual, other).Scan(&outstanding, &effects); e != nil {
			return false, e
		}
		if effects == 2 && outstanding > 0 {
			return false, ErrMergePending
		}
	}
	for _, c := range children {
		actual, e := resolveJobID(ctx, tx, c.id)
		if e != nil {
			return false, e
		}
		var other int64
		var otherState string
		e = tx.QueryRowContext(ctx, `SELECT id,state FROM representations WHERE request_id=? AND format=?`, canonical, c.format).Scan(&other, &otherState)
		if errors.Is(e, sql.ErrNoRows) {
			if _, e = tx.ExecContext(ctx, `UPDATE representations SET request_id=? WHERE id=?`, canonical, c.id); e != nil {
				return false, e
			}
			continue
		}
		if e != nil {
			return false, e
		}
		other, e = resolveJobID(ctx, tx, other)
		if e != nil {
			return false, e
		}
		if other == actual {
			continue
		}
		if e = tx.QueryRowContext(ctx, `SELECT state FROM representations WHERE id=?`, other).Scan(&otherState); e != nil {
			return false, e
		}
		owner, follower := other, actual
		var actualState string
		if e = tx.QueryRowContext(ctx, `SELECT state FROM representations WHERE id=?`, actual).Scan(&actualState); e != nil {
			return false, e
		}
		var hasIntent int
		tx.QueryRowContext(ctx, `SELECT count(*) FROM transfer_intents WHERE representation_id=? AND state!='rejected'`, actual).Scan(&hasIntent)
		if (actualState == "available" && otherState != "available") || (hasIntent > 0 && (otherState == "queued" || otherState == "missing" || otherState == "waiting_library" || otherState == "waiting_allowance" || otherState == "canceled")) {
			owner, follower = actual, other
		}
		var actualUnresolved, otherUnresolved int
		if e = tx.QueryRowContext(ctx, `SELECT count(*) FROM transfer_intents WHERE representation_id=? AND state='unresolved'`, actual).Scan(&actualUnresolved); e != nil {
			return false, e
		}
		if e = tx.QueryRowContext(ctx, `SELECT count(*) FROM transfer_intents WHERE representation_id=? AND state='unresolved'`, other).Scan(&otherUnresolved); e != nil {
			return false, e
		}
		if actualUnresolved > 0 {
			owner, follower = actual, other
		} else if otherUnresolved > 0 {
			owner, follower = other, actual
		}
		var discardable int
		if e = tx.QueryRowContext(ctx, `SELECT count(*) FROM transfer_intents WHERE representation_id=? AND state='unresolved'`, follower).Scan(&discardable); e != nil {
			return false, e
		}
		if discardable > 0 {
			return false, ErrMergePending
		}
		if owner == other && otherState == "canceled" && actualState != "canceled" {
			if e = activateCanceled(ctx, tx, owner); e != nil {
				return false, e
			}
		}
		if _, e = tx.ExecContext(ctx, `INSERT OR REPLACE INTO linked_jobs VALUES(?,?)`, follower, owner); e != nil {
			return false, e
		}
		if _, e = tx.ExecContext(ctx, `UPDATE linked_jobs SET canonical=? WHERE canonical=?`, owner, follower); e != nil {
			return false, e
		}
		if _, e = tx.ExecContext(ctx, `INSERT INTO job_bumps(job_id,rank) SELECT ?,rank FROM job_bumps WHERE job_id=? ON CONFLICT(job_id) DO UPDATE SET rank=MAX(job_bumps.rank,excluded.rank)`, owner, follower); e != nil {
			return false, e
		}
		if _, e = tx.ExecContext(ctx, `DELETE FROM job_bumps WHERE job_id=?`, follower); e != nil {
			return false, e
		}
		if _, e = tx.ExecContext(ctx, `UPDATE representations SET state='linked',lease_token=NULL,lease_until=NULL WHERE id=?`, follower); e != nil {
			return false, e
		}
		if follower == l.ID {
			keepCurrent = false
		}
	}
	if _, e = tx.ExecContext(ctx, `UPDATE work_aliases SET request_id=? WHERE request_id=?`, canonical, l.RequestID); e != nil {
		return false, e
	}
	for _, a := range aliases {
		if _, e = tx.ExecContext(ctx, `INSERT OR IGNORE INTO work_aliases VALUES(?,?)`, a, canonical); e != nil {
			return false, e
		}
	}
	if _, e = tx.ExecContext(ctx, `UPDATE requests SET rank=max(rank,(SELECT rank FROM requests WHERE id=?)),bump=max(bump,(SELECT bump FROM requests WHERE id=?)) WHERE id=?`, l.RequestID, l.RequestID, canonical); e != nil {
		return false, e
	}
	if _, e = tx.ExecContext(ctx, `INSERT OR REPLACE INTO merged_requests VALUES(?,?)`, l.RequestID, canonical); e != nil {
		return false, e
	}
	if _, e = tx.ExecContext(ctx, `UPDATE merged_requests SET canonical=? WHERE canonical=?`, canonical, l.RequestID); e != nil {
		return false, e
	}
	return keepCurrent, tx.Commit()
}
