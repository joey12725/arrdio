package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"time"
)

type OwnedPlacement struct{ Path, Digest, Pending string }

var ErrPlacementConflict = errors.New("unowned, modified, or protected library file was preserved")

// ImportHooks keeps filesystem publication fenced by durable job ownership.
// Replace applies only to an explicit correction, never ordinary intake/cancel.
type ImportHooks struct {
	Check         func() error
	Lookup        func(string) (OwnedPlacement, error)
	Begin         func(string, string, string) error
	Commit        func(string, string) error
	Owned         func() ([]OwnedPlacement, error)
	Retired       func(string) error
	Replace       bool
	Ordered       bool
	Protected     map[string]bool
	ProtectedFile func(string) bool
	Dirty         func(string) error
	ClaimRefresh  func(string) (bool, error)
	FinishRefresh func(string, bool) error
}
type importHooksKey struct{}

func WithImportHooks(ctx context.Context, hooks *ImportHooks) context.Context {
	return context.WithValue(ctx, importHooksKey{}, hooks)
}
func GetImportHooks(ctx context.Context) *ImportHooks {
	hooks, _ := ctx.Value(importHooksKey{}).(*ImportHooks)
	return hooks
}
func CheckImport(ctx context.Context) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	if h := GetImportHooks(ctx); h != nil && h.Check != nil {
		return h.Check()
	}
	return nil
}

func (r *Runtime) importContext(ctx context.Context, l Lease) context.Context {
	var active int
	r.Store.db.QueryRowContext(ctx, `SELECT active FROM correction_requests WHERE job_id=?`, l.ID).Scan(&active)
	hooks := &ImportHooks{Replace: active == 1}
	hooks.Protected = map[string]bool{}
	var receiptBytes []byte
	r.Store.db.QueryRowContext(ctx, `SELECT receipt FROM job_details WHERE id=?`, l.ID).Scan(&receiptBytes)
	var receipt Receipt
	json.Unmarshal(receiptBytes, &receipt)
	for _, p := range append(receipt.Paths, receipt.OriginalPaths...) {
		hooks.Protected[p] = true
	}
	hooks.Ordered = receipt.Ordered
	protectedInfo := []os.FileInfo{}
	for p := range hooks.Protected {
		if info, e := os.Stat(p); e == nil {
			protectedInfo = append(protectedInfo, info)
		}
	}
	hooks.ProtectedFile = func(path string) bool {
		if hooks.Protected[path] {
			return true
		}
		info, e := os.Stat(path)
		if e != nil {
			return false
		}
		for _, source := range protectedInfo {
			if os.SameFile(info, source) {
				return true
			}
		}
		return false
	}
	hooks.Check = func() error {
		if !r.Store.owns(ctx, l) {
			return errors.New("import ownership lost")
		}
		return ctx.Err()
	}
	hooks.Lookup = func(path string) (OwnedPlacement, error) {
		var p OwnedPlacement
		p.Path = path
		e := r.Store.db.QueryRowContext(ctx, `SELECT digest,COALESCE(pending_digest,'') FROM placements WHERE destination=? AND job_id=?`, path, l.ID).Scan(&p.Digest, &p.Pending)
		if errors.Is(e, sql.ErrNoRows) {
			return OwnedPlacement{}, nil
		}
		return p, e
	}
	hooks.Begin = func(source, path, digest string) error {
		if e := hooks.Check(); e != nil {
			return e
		}
		result, e := r.Store.db.ExecContext(ctx, `INSERT INTO placements(destination,job_id,source,digest,pending_digest) VALUES(?,?,?,'',?) ON CONFLICT(destination) DO UPDATE SET source=excluded.source,pending_digest=excluded.pending_digest WHERE placements.job_id=excluded.job_id`, path, l.ID, source, digest)
		if e != nil {
			return e
		}
		n, _ := result.RowsAffected()
		if n != 1 {
			return ErrConflict
		}
		return nil
	}
	hooks.Commit = func(path, digest string) error {
		saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		result, e := r.Store.db.ExecContext(saveCtx, `UPDATE placements SET digest=?,pending_digest=NULL WHERE destination=? AND job_id=? AND pending_digest=?`, digest, path, l.ID, digest)
		if e != nil {
			return e
		}
		n, _ := result.RowsAffected()
		if n == 1 {
			return nil
		}
		var recorded string
		if r.Store.db.QueryRowContext(saveCtx, `SELECT digest FROM placements WHERE destination=? AND job_id=?`, path, l.ID).Scan(&recorded) == nil && recorded == digest {
			return nil
		}
		return ErrConflict
	}
	hooks.Owned = func() ([]OwnedPlacement, error) {
		rows, e := r.Store.db.QueryContext(ctx, `SELECT destination,digest,COALESCE(pending_digest,'') FROM placements WHERE job_id=? AND digest!=''`, l.ID)
		if e != nil {
			return nil, e
		}
		defer rows.Close()
		out := []OwnedPlacement{}
		for rows.Next() {
			var p OwnedPlacement
			if e = rows.Scan(&p.Path, &p.Digest, &p.Pending); e != nil {
				return nil, e
			}
			out = append(out, p)
		}
		return out, rows.Err()
	}
	hooks.Retired = func(path string) error {
		saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_, e := r.Store.db.ExecContext(saveCtx, `DELETE FROM placements WHERE destination=? AND job_id=?`, path, l.ID)
		return e
	}
	hooks.Dirty = func(key string) error {
		_, e := r.Store.db.ExecContext(ctx, `INSERT INTO library_refresh(library_key,dirty) VALUES(?,1) ON CONFLICT(library_key) DO UPDATE SET dirty=dirty+1`, key)
		return e
	}
	claims := map[string]string{}
	hooks.ClaimRefresh = func(key string) (bool, error) {
		tx, e := r.Store.db.BeginTx(ctx, nil)
		if e != nil {
			return false, e
		}
		defer tx.Rollback()
		result, e := tx.ExecContext(ctx, `UPDATE library_refresh SET lease_until=? WHERE library_key=? AND dirty>0 AND requested_at<=? AND lease_until<=?`, time.Now().Add(time.Minute).Unix(), key, time.Now().Add(-5*time.Minute).Unix(), time.Now().Unix())
		if e != nil {
			return false, e
		}
		n, e := result.RowsAffected()
		if e != nil || n != 1 {
			return false, e
		}
		token := randomToken()
		if _, e = tx.ExecContext(ctx, `INSERT INTO library_refresh_claims(library_key,token,generation) SELECT library_key,?,dirty FROM library_refresh WHERE library_key=? ON CONFLICT(library_key) DO UPDATE SET token=excluded.token,generation=excluded.generation`, token, key); e != nil {
			return false, e
		}
		if e = tx.Commit(); e != nil {
			return false, e
		}
		claims[key] = token
		return true, nil
	}
	hooks.FinishRefresh = func(key string, success bool) error {
		_, e := r.Store.db.ExecContext(ctx, `UPDATE library_refresh SET dirty=CASE WHEN ? AND dirty=(SELECT generation FROM library_refresh_claims WHERE library_key=?) THEN 0 ELSE dirty END,requested_at=?,lease_until=0 WHERE library_key=? AND EXISTS(SELECT 1 FROM library_refresh_claims WHERE library_key=? AND token=?)`, success, key, time.Now().Unix(), key, key, claims[key])
		return e
	}
	return WithImportHooks(ctx, hooks)
}
