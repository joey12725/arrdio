package jobs

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"strings"
)

type StoredRoute struct{ Category, Format, Service, LibraryID, Path string }

func (r *Runtime) StoredRoutes(ctx context.Context) ([]StoredRoute, error) {
	rows, e := r.Store.db.QueryContext(ctx, `SELECT category,format,library,library_id,path FROM routing ORDER BY rowid`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []StoredRoute{}
	for rows.Next() {
		var v StoredRoute
		if e = rows.Scan(&v.Category, &v.Format, &v.Service, &v.LibraryID, &v.Path); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

type MigrationResult struct {
	Imported int `json:"imported"`
	Existing int `json:"existing"`
	Failed   int `json:"failed"`
}

// ImportLegacyAccounts only reads the explicitly supplied offline ARRDIO backup.
// It never infers a production database path or imports arbitrary old save paths.
func (r *Runtime) ImportLegacyAccounts(ctx context.Context, backup string) (MigrationResult, error) {
	result := MigrationResult{}
	db, e := sql.Open("sqlite3", "file:"+url.PathEscape(backup)+"?mode=ro")
	if e != nil {
		return result, e
	}
	defer db.Close()
	rows, e := db.QueryContext(ctx, `SELECT email,password FROM zlibuser ORDER BY id LIMIT 1001`)
	if e != nil {
		return result, fmt.Errorf("offline ARRDIO account table unavailable")
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		count++
		if count > 1000 {
			return result, ErrInvalid
		}
		var email, password string
		if e = rows.Scan(&email, &password); e != nil {
			return result, fmt.Errorf("legacy account record invalid")
		}
		email = strings.ToLower(strings.TrimSpace(email))
		var exists int
		if r.Store.db.QueryRowContext(ctx, `SELECT 1 FROM accounts WHERE email=?`, email).Scan(&exists) == nil {
			result.Existing++
			continue
		}
		if e = r.AddAccount(ctx, email, password); e != nil {
			result.Failed++
			continue
		}
		result.Imported++
	}
	return result, rows.Err()
}

// Backup uses SQLite's consistent snapshot facility for this application's own DB.
func (s *Store) Backup(ctx context.Context, destination string) error {
	f, e := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	f.Close()
	if _, e = s.db.ExecContext(ctx, `VACUUM INTO ?`, destination); e != nil {
		os.Remove(destination)
		return e
	}
	return nil
}
