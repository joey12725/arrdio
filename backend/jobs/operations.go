package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

func (r *Runtime) Categorize(ctx context.Context, ids []int64, category string) error {
	if len(ids) == 0 || len(ids) > 1000 {
		return ErrInvalid
	}
	tx, e := r.Store.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var format string
	if e = tx.QueryRowContext(ctx, `SELECT format FROM routing WHERE category=?`, category).Scan(&format); e != nil {
		return ErrInvalid
	}
	for _, rid := range ids {
		var canonical int64
		if tx.QueryRowContext(ctx, `SELECT canonical FROM merged_requests WHERE id=?`, rid).Scan(&canonical) == nil {
			rid = canonical
		}
		id, e := representationOwner(ctx, tx, rid, format)
		if e != nil {
			return ErrInvalid
		}
		var state string
		var leaseUntil int64
		if e = tx.QueryRowContext(ctx, `SELECT state,COALESCE(lease_until,0) FROM representations WHERE id=?`, id).Scan(&state, &leaseUntil); e != nil {
			return ErrInvalid
		}
		if state == "importing" && leaseUntil > time.Now().Unix() {
			return ErrConflict
		}
		if state == "available" {
			var data []byte
			tx.QueryRowContext(ctx, `SELECT receipt FROM job_details WHERE id=?`, id).Scan(&data)
			var receipt Receipt
			if json.Unmarshal(data, &receipt) != nil || len(receipt.Paths) == 0 {
				return ErrConflict
			}
			if _, e = tx.ExecContext(ctx, `UPDATE representations SET state='importing',eligible_at=0 WHERE id=?`, id); e != nil {
				return e
			}
		}
		if _, e = tx.ExecContext(ctx, `INSERT INTO representation_categories VALUES(?,?) ON CONFLICT(job_id) DO UPDATE SET category=excluded.category`, id, category); e != nil {
			return e
		}
	}
	return tx.Commit()
}
func (r *Runtime) Candidates(ctx context.Context, id int64, format string) ([]Candidate, error) {
	id = r.Store.canonicalID(ctx, id)
	if format != "ebook" && format != "audio" && format != "manga" {
		return nil, ErrInvalid
	}
	in, e := r.Store.input(ctx, id)
	if e != nil {
		return nil, e
	}
	job, e := representationOwner(ctx, r.Store.db, id, format)
	if e != nil {
		return nil, e
	}
	out := []Candidate{}
	for _, source := range r.Sources {
		candidates, e := source.Search(ctx, in, format)
		if e != nil {
			continue
		}
		for _, c := range candidates {
			if !in.Allows(c, format) {
				continue
			}
			data, _ := json.Marshal(struct {
				Candidate Candidate
				URL       string
				Extra     json.RawMessage
			}{c, c.URL, c.Extra})
			key := tokenHash(string(data))
			if _, e = r.Store.db.ExecContext(ctx, `INSERT INTO candidate_options VALUES(?,?,?,?) ON CONFLICT(job_id,key) DO UPDATE SET data=excluded.data,expires=excluded.expires`, job, key, data, time.Now().Add(time.Hour).Unix()); e != nil {
				return nil, e
			}
			c.ID = key
			out = append(out, c)
		}
	}
	return out, nil
}
func (r *Runtime) Correct(ctx context.Context, id int64, format, key string) error {
	id = r.Store.canonicalID(ctx, id)
	tx, e := r.Store.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	job, e := representationOwner(ctx, tx, id, format)
	if e != nil {
		return e
	}
	var state string
	if e = tx.QueryRowContext(ctx, `SELECT state FROM representations WHERE id=?`, job).Scan(&state); e != nil {
		return e
	}
	var unresolved int
	tx.QueryRowContext(ctx, `SELECT count(*) FROM transfer_intents WHERE representation_id=? AND state='unresolved'`, job).Scan(&unresolved)
	if unresolved > 0 || state == "matching" || state == "transferring" || state == "importing" {
		return ErrConflict
	}
	var requirements []byte
	var constraints Constraints
	if tx.QueryRowContext(ctx, `SELECT data FROM request_constraints WHERE request_id=?`, id).Scan(&requirements) == nil {
		json.Unmarshal(requirements, &constraints)
	}
	var data []byte
	if e = tx.QueryRowContext(ctx, `SELECT data FROM candidate_options WHERE job_id=? AND key=? AND expires>?`, job, key, time.Now().Unix()).Scan(&data); e != nil {
		return ErrInvalid
	}
	var choice struct{ Candidate Candidate }
	if json.Unmarshal(data, &choice) != nil || !(Input{Constraints: constraints}).Allows(choice.Candidate, format) {
		return ErrInvalid
	}
	for _, query := range []string{`INSERT INTO intent_history(representation_id,provider,candidate_id,receipt,state) SELECT representation_id,provider,candidate_id,receipt,state FROM transfer_intents WHERE representation_id=?`, `DELETE FROM transfer_intents WHERE representation_id=?`, `DELETE FROM reservations WHERE job_id=?`, `DELETE FROM conversions WHERE job_id=?`, `DELETE FROM retained_files WHERE job_id=?`} {
		if _, e = tx.ExecContext(ctx, query, job); e != nil {
			return e
		}
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO candidate_choices VALUES(?,?) ON CONFLICT(job_id) DO UPDATE SET candidate=excluded.candidate`, job, data); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO correction_requests VALUES(?,1) ON CONFLICT(job_id) DO UPDATE SET active=1`, job); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, `UPDATE representations SET state='queued',eligible_at=0 WHERE id=?`, job); e != nil {
		return e
	}
	return tx.Commit()
}

func (r *Runtime) Refresh(ctx context.Context, id int64, format string) error {
	id = r.Store.canonicalID(ctx, id)
	job, e := representationOwner(ctx, r.Store.db, id, format)
	if e != nil {
		return ErrInvalid
	}
	service, ok := r.Library.(interface {
		Refresh(context.Context, string, string) error
	})
	if !ok {
		return errors.New("library refresh unavailable")
	}
	return service.Refresh(ctx, format, r.category(ctx, Lease{ID: job}))
}
