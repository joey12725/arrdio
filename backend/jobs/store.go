// Package jobs owns durable acquisition intent independently of HTTP and providers.
package jobs

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	_ "github.com/mattn/go-sqlite3"
	"net/url"
	"strings"
	"time"
)

var ErrConflict = errors.New("idempotency key already used with different input")
var ErrInvalid = errors.New("invalid request")

type Store struct{ db *sql.DB }
type Input struct {
	Title       string      `json:"title"`
	Author      string      `json:"author"`
	CatalogID   string      `json:"catalog_id,omitempty"`
	Volume      string      `json:"volume,omitempty"`
	Kind        string      `json:"kind,omitempty"`
	Formats     []string    `json:"formats,omitempty"`
	Constraints Constraints `json:"constraints,omitempty"`
}
type Batch struct {
	ID         int64       `json:"id"`
	RequestIDs []int64     `json:"request_ids"`
	Errors     []ItemError `json:"errors,omitempty"`
}
type ItemError struct {
	Index int    `json:"index"`
	Error string `json:"error"`
}
type Representation struct {
	ID              int64      `json:"id"`
	Format          string     `json:"format"`
	State           string     `json:"state"`
	Error           string     `json:"error,omitempty"`
	Presence        []Presence `json:"presence,omitempty"`
	Conversion      string     `json:"conversion,omitempty"`
	ConversionError string     `json:"conversion_error,omitempty"`
	Selected        *Candidate `json:"selected,omitempty"`
}
type Request struct {
	ID              int64            `json:"id"`
	Title           string           `json:"title"`
	Author          string           `json:"author"`
	CatalogID       string           `json:"catalog_id,omitempty"`
	Volume          string           `json:"volume,omitempty"`
	Kind            string           `json:"kind"`
	Representations []Representation `json:"representations"`
	Cover           string           `json:"cover,omitempty"`
	Description     string           `json:"description,omitempty"`
	Constraints     Constraints      `json:"constraints,omitempty"`
}

// Open uses a separate application database, never a library database.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite3", "file:"+url.PathEscape(path)+"?_foreign_keys=on&_journal_mode=WAL&_busy_timeout=5000&_txlock=immediate")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	var hasSchema int
	if err = db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='job_schema'`).Scan(&hasSchema); err != nil {
		db.Close()
		return nil, err
	}
	if hasSchema > 0 {
		var version int
		if err = db.QueryRow(`SELECT COALESCE(max(version),0) FROM job_schema`).Scan(&version); err != nil {
			db.Close()
			return nil, err
		}
		if version > 2 {
			db.Close()
			return nil, errors.New("database schema is newer than this binary")
		}
	}
	s := &Store{db: db}
	_, err = db.Exec(`
CREATE TABLE IF NOT EXISTS job_schema (version INTEGER PRIMARY KEY);
INSERT OR IGNORE INTO job_schema VALUES (1);
CREATE TABLE IF NOT EXISTS job_sequence (id INTEGER PRIMARY KEY CHECK(id=1), value INTEGER NOT NULL);
INSERT OR IGNORE INTO job_sequence VALUES (1,0);
CREATE TABLE IF NOT EXISTS requests (
 id INTEGER PRIMARY KEY, identity TEXT NOT NULL UNIQUE, title TEXT NOT NULL,
 author TEXT NOT NULL, catalog_id TEXT NOT NULL, volume TEXT NOT NULL, kind TEXT NOT NULL,
 rank INTEGER NOT NULL, bump INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS representations (
 id INTEGER PRIMARY KEY, request_id INTEGER NOT NULL REFERENCES requests(id),
 format TEXT NOT NULL CHECK(format IN ('ebook','audio','manga')),
 state TEXT NOT NULL DEFAULT 'queued', eligible_at INTEGER NOT NULL DEFAULT 0,
 lease_token TEXT, lease_until INTEGER, attempts INTEGER NOT NULL DEFAULT 0,
 UNIQUE(request_id,format));
CREATE INDEX IF NOT EXISTS dispatch ON representations(state,eligible_at);
CREATE INDEX IF NOT EXISTS request_order ON requests(bump DESC,rank DESC);
CREATE TABLE IF NOT EXISTS batches (
 id INTEGER PRIMARY KEY, principal TEXT NOT NULL, key TEXT NOT NULL, digest TEXT NOT NULL,
 result TEXT NOT NULL DEFAULT '', UNIQUE(principal,key));
CREATE TABLE IF NOT EXISTS transfer_intents (
 representation_id INTEGER PRIMARY KEY REFERENCES representations(id),
 provider TEXT NOT NULL, candidate_id TEXT NOT NULL, receipt TEXT,
 state TEXT NOT NULL DEFAULT 'unresolved');
`)
	if err != nil {
		db.Close()
		return nil, err
	}
	if err = s.migrateRuntime(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}
func (s *Store) Close() error    { return s.db.Close() }
func normalized(v string) string { return strings.ToLower(strings.Join(strings.Fields(v), " ")) }
func identity(in Input) string {
	parts := []string{in.Kind, normalized(in.Title), normalized(in.Author), normalized(in.Volume)}
	if !in.Constraints.Empty() {
		raw, _ := json.Marshal(in.Constraints)
		parts = append(parts, string(raw))
	}
	b, _ := json.Marshal(parts)
	return string(b)
}
func validate(in *Input) error {
	for _, value := range []*string{&in.Constraints.Language, &in.Constraints.Narrator, &in.Constraints.Edition, &in.Constraints.Container} {
		*value = normalizeConstraint(*value)
		if len(*value) > 200 {
			return ErrInvalid
		}
	}
	in.Title = strings.TrimSpace(in.Title)
	in.Author = strings.TrimSpace(in.Author)
	if in.Title == "" || len(in.Title) > 1000 || len(in.Author) > 1000 || len(in.CatalogID) > 500 || len(in.Volume) > 100 {
		return ErrInvalid
	}
	if in.Kind == "" {
		in.Kind = "book"
	}
	if in.Kind != "book" && in.Kind != "manga" {
		return ErrInvalid
	}
	if len(in.Formats) == 0 {
		if in.Kind == "manga" {
			in.Formats = []string{"manga"}
		} else {
			in.Formats = []string{"ebook", "audio"}
		}
	}
	if len(in.Formats) > 3 {
		return ErrInvalid
	}
	for _, f := range in.Formats {
		if f != "ebook" && f != "audio" && f != "manga" {
			return ErrInvalid
		}
	}
	return nil
}
func sequence(ctx context.Context, tx *sql.Tx) (int64, error) {
	var n int64
	err := tx.QueryRowContext(ctx, `UPDATE job_sequence SET value=value+1 WHERE id=1 RETURNING value`).Scan(&n)
	return n, err
}

// Enqueue commits a bounded batch atomically. Replays never touch queue ranks.
func (s *Store) Enqueue(ctx context.Context, principal, key string, inputs []Input) (Batch, error) {
	if principal == "" || key == "" || len(key) > 200 || len(inputs) == 0 || len(inputs) > 10000 {
		return Batch{}, ErrInvalid
	}
	raw, _ := json.Marshal(inputs)
	inputs = append([]Input(nil), inputs...)
	digest := sha256.Sum256(raw)
	hash := hex.EncodeToString(digest[:])
	invalid := map[int]bool{}
	var itemErrors []ItemError
	for i := range inputs {
		if e := validate(&inputs[i]); e != nil {
			invalid[i] = true
			itemErrors = append(itemErrors, ItemError{Index: i, Error: "invalid book request"})
		}
	}
	if len(invalid) == len(inputs) {
		return Batch{}, ErrInvalid
	}
	normalizedRaw, _ := json.Marshal(inputs)
	legacyDigest := sha256.Sum256(normalizedRaw)
	legacyHash := hex.EncodeToString(legacyDigest[:])
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Batch{}, err
	}
	defer tx.Rollback()
	var previous, result string
	err = tx.QueryRowContext(ctx, `SELECT digest,result FROM batches WHERE principal=? AND key=?`, principal, key).Scan(&previous, &result)
	if err == nil {
		if previous != hash && previous != legacyHash {
			return Batch{}, ErrConflict
		}
		var b Batch
		err = json.Unmarshal([]byte(result), &b)
		return b, err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Batch{}, err
	}
	r, err := tx.ExecContext(ctx, `INSERT INTO batches(principal,key,digest) VALUES(?,?,?)`, principal, key, hash)
	if err != nil {
		return Batch{}, err
	}
	id, _ := r.LastInsertId()
	batch := Batch{ID: id, RequestIDs: make([]int64, 0, len(inputs)), Errors: itemErrors}
	for index, in := range inputs {
		if invalid[index] {
			continue
		}
		n, err := sequence(ctx, tx)
		if err != nil {
			return Batch{}, err
		}
		var rid int64
		aliases := []string{identity(in)}
		if in.CatalogID != "" {
			aliases = append(aliases, "catalog:"+in.CatalogID+":"+normalized(in.Volume)+in.Constraints.scope())
		}
		for _, alias := range aliases {
			if tx.QueryRowContext(ctx, `SELECT request_id FROM work_aliases WHERE alias=?`, alias).Scan(&rid) == nil {
				break
			}
		}
		if rid == 0 {
			err = tx.QueryRowContext(ctx, `INSERT INTO requests(identity,title,author,catalog_id,volume,kind,rank) VALUES(?,?,?,?,?,?,?)
ON CONFLICT(identity) DO UPDATE SET rank=excluded.rank RETURNING id`, identity(in), in.Title, in.Author, in.CatalogID, in.Volume, in.Kind, n).Scan(&rid)
		} else {
			_, err = tx.ExecContext(ctx, `UPDATE requests SET rank=?,catalog_id=CASE WHEN catalog_id='' THEN ? ELSE catalog_id END WHERE id=?`, n, in.CatalogID, rid)
		}
		if err != nil {
			return Batch{}, err
		}
		constraintData, _ := json.Marshal(in.Constraints)
		if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO request_constraints VALUES(?,?)`, rid, constraintData); err != nil {
			return Batch{}, err
		}
		for _, alias := range aliases {
			if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO work_aliases VALUES(?,?)`, alias, rid); err != nil {
				return Batch{}, err
			}
		}
		for _, format := range in.Formats {
			if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO representations(request_id,format) VALUES(?,?)`, rid, format); err != nil {
				return Batch{}, err
			}
			owner, e := representationOwner(ctx, tx, rid, format)
			if e != nil {
				return Batch{}, e
			}
			if e = activateCanceled(ctx, tx, owner); e != nil {
				return Batch{}, e
			}
		}
		batch.RequestIDs = append(batch.RequestIDs, rid)
	}
	encoded, _ := json.Marshal(batch)
	if _, err = tx.ExecContext(ctx, `UPDATE batches SET result=? WHERE id=?`, string(encoded), batch.ID); err != nil {
		return Batch{}, err
	}
	return batch, tx.Commit()
}

func (s *Store) List(ctx context.Context, limit, offset int, sort string, filters ...string) ([]Request, error) {
	if limit < 1 || limit > 200 || offset < 0 {
		return nil, ErrInvalid
	}
	orders := map[string]string{"queue": "MAX(bump,COALESCE((SELECT MAX(b.rank) FROM job_bumps b JOIN representations j ON j.id=b.job_id WHERE COALESCE((SELECT canonical FROM merged_requests WHERE id=j.request_id),j.request_id)=requests.id),0)) DESC,rank DESC,id DESC", "title": "title COLLATE NOCASE,id", "-title": "title COLLATE NOCASE DESC,id", "author": "author COLLATE NOCASE,id", "-author": "author COLLATE NOCASE DESC,id"}
	order, ok := orders[sort]
	if !ok {
		return nil, ErrInvalid
	}
	filter := ""
	if len(filters) > 0 {
		filter = filters[0]
	}
	if len(filter) > 500 {
		return nil, ErrInvalid
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,title,author,catalog_id,volume,kind FROM requests WHERE id NOT IN (SELECT id FROM merged_requests) AND (?='' OR instr(lower(title || ' ' || author),lower(?))>0) ORDER BY `+order+` LIMIT ? OFFSET ?`, filter, filter, limit, offset)
	if err != nil {
		return nil, err
	}
	requests := []Request{}
	for rows.Next() {
		var r Request
		if err = rows.Scan(&r.ID, &r.Title, &r.Author, &r.CatalogID, &r.Volume, &r.Kind); err != nil {
			rows.Close()
			return nil, err
		}
		requests = append(requests, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for i := range requests {
		r := &requests[i]
		if c, e := s.constraints(ctx, r.ID); e == nil {
			r.Constraints = c
		}
		s.db.QueryRowContext(ctx, `SELECT cover,description FROM request_metadata WHERE request_id=?`, r.ID).Scan(&r.Cover, &r.Description)
		r.Representations = []Representation{}
		children, err := s.db.QueryContext(ctx, `SELECT id,format,state FROM representations WHERE request_id=? ORDER BY id`, r.ID)
		if err != nil {
			return nil, err
		}
		for children.Next() {
			var c Representation
			if err = children.Scan(&c.ID, &c.Format, &c.State); err != nil {
				children.Close()
				return nil, err
			}
			r.Representations = append(r.Representations, c)
		}
		err = children.Err()
		children.Close()
		if err != nil {
			return nil, err
		}
		for j := range r.Representations {
			c := &r.Representations[j]
			owner, e := resolveJobID(ctx, s.db, c.ID)
			if e != nil {
				return nil, e
			}
			c.ID = owner
			if e = s.db.QueryRowContext(ctx, `SELECT state FROM representations WHERE id=?`, owner).Scan(&c.State); e != nil {
				return nil, e
			}
			var presence, candidate []byte
			s.db.QueryRowContext(ctx, `SELECT error,presence,candidate FROM job_details WHERE id=?`, c.ID).Scan(&c.Error, &presence, &candidate)
			json.Unmarshal(presence, &c.Presence)
			s.db.QueryRowContext(ctx, `SELECT state,error FROM conversions WHERE job_id=?`, c.ID).Scan(&c.Conversion, &c.ConversionError)
			var detail struct{ Candidate Candidate }
			if json.Unmarshal(candidate, &detail) == nil {
				if detail.Candidate.ID != "" {
					public := detail.Candidate
					public.ID = tokenHash(public.Provider + ":" + public.ID)
					c.Selected = &public
				}
				if r.Cover == "" {
					r.Cover = detail.Candidate.Cover
				}
				if r.Description == "" {
					r.Description = detail.Candidate.Description
				}
			}
		}
	}
	return requests, nil
}
func (s *Store) Counts(ctx context.Context) (map[string]int64, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT state,count(*) FROM representations WHERE state!='linked' GROUP BY state`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var state string
		var n int64
		if err = rows.Scan(&state, &n); err != nil {
			return nil, err
		}
		out[state] = n
	}
	return out, rows.Err()
}

// Control targets a parent or one representation. Available content is never deleted.
func (s *Store) Control(ctx context.Context, id int64, format, action string) error {
	if id < 1 || (format != "" && format != "ebook" && format != "audio" && format != "manga") {
		return ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var canonical int64
	if tx.QueryRowContext(ctx, `SELECT canonical FROM merged_requests WHERE id=?`, id).Scan(&canonical) == nil {
		id = canonical
	}
	var exists int
	if err = tx.QueryRowContext(ctx, `SELECT 1 FROM requests WHERE id=?`, id).Scan(&exists); err != nil {
		return err
	}
	owners := []int64{}
	if action == "cancel" || action == "retry" {
		rows, e := tx.QueryContext(ctx, `SELECT id FROM representations WHERE request_id=? AND (?='' OR format=?)`, id, format, format)
		if e != nil {
			return e
		}
		direct := []int64{}
		for rows.Next() {
			var child int64
			if e = rows.Scan(&child); e != nil {
				rows.Close()
				return e
			}
			direct = append(direct, child)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		seen := map[int64]bool{}
		for _, child := range direct {
			owner, e := resolveJobID(ctx, tx, child)
			if e != nil {
				return e
			}
			if !seen[owner] {
				owners = append(owners, owner)
				seen[owner] = true
			}
		}
	}
	switch action {
	case "bump":
		n, e := sequence(ctx, tx)
		if e != nil {
			return e
		}
		if format == "" {
			_, err = tx.ExecContext(ctx, `UPDATE requests SET bump=? WHERE id=?`, n, id)
		} else {
			owner, e := representationOwner(ctx, tx, id, format)
			if e != nil {
				return e
			}
			_, err = tx.ExecContext(ctx, `INSERT INTO job_bumps VALUES(?,?) ON CONFLICT(job_id) DO UPDATE SET rank=excluded.rank`, owner, n)
		}
	case "cancel":
		for _, owner := range owners {
			if _, err = tx.ExecContext(ctx, `UPDATE representations SET state='canceled',lease_token=NULL,lease_until=NULL WHERE id=? AND state!='available'`, owner); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `DELETE FROM reservations WHERE job_id=? AND EXISTS(SELECT 1 FROM representations WHERE id=? AND state='canceled') AND NOT EXISTS(SELECT 1 FROM transfer_intents WHERE representation_id=job_id AND state='unresolved')`, owner, owner); err != nil {
				return err
			}
		}
	case "retry":
		for _, owner := range owners {
			if _, err = tx.ExecContext(ctx, `UPDATE representations SET state='queued',eligible_at=0 WHERE id=? AND state IN ('retryable_failure','missing') AND NOT EXISTS(SELECT 1 FROM transfer_intents WHERE representation_id=representations.id AND state='unresolved')`, owner); err != nil {
				return err
			}
		}
	default:
		return ErrInvalid
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}

type Lease struct {
	ID        int64
	RequestID int64
	Format    string
	Token     string
}

// Claim does not redispatch ambiguous external operations; reconcile their intents first.
func (s *Store) Claim(ctx context.Context, format, token string, now time.Time, ttl time.Duration) (Lease, error) {
	if token == "" || ttl < time.Second {
		return Lease{}, ErrInvalid
	}
	var l Lease
	l.Token = token
	err := s.db.QueryRowContext(ctx, `UPDATE representations SET state='matching',lease_token=?,lease_until=?,attempts=attempts+1
WHERE id=(SELECT j.id FROM representations j JOIN requests r ON r.id=COALESCE((SELECT canonical FROM merged_requests WHERE id=j.request_id),j.request_id)
 WHERE j.format=? AND j.eligible_at<=? AND (j.state='queued' OR (j.state='matching' AND j.lease_until<=?))
 AND NOT EXISTS(SELECT 1 FROM transfer_intents t WHERE t.representation_id=j.id AND t.state='unresolved')
 ORDER BY MAX(r.bump,COALESCE((SELECT rank FROM job_bumps WHERE job_id=j.id),0)) DESC,r.rank DESC,j.id DESC LIMIT 1)
RETURNING id,request_id,format`, token, now.Add(ttl).Unix(), format, now.Unix(), now.Unix()).Scan(&l.ID, &l.RequestID, &l.Format)
	return l, err
}

// RecordIntent commits before a provider's potentially consuming operation.
func (s *Store) RecordIntent(ctx context.Context, l Lease, provider, candidate string, now time.Time) error {
	if provider == "" || candidate == "" {
		return ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	r, err := tx.ExecContext(ctx, `INSERT INTO transfer_intents(representation_id,provider,candidate_id)
	 SELECT id,?,? FROM representations WHERE id=? AND lease_token=? AND lease_until>? AND state='matching' ON CONFLICT(representation_id) DO UPDATE SET provider=excluded.provider,candidate_id=excluded.candidate_id,receipt=NULL,state='unresolved' WHERE transfer_intents.state='rejected'`, provider, candidate, l.ID, l.Token, now.Unix())
	if err != nil {
		return err
	}
	n, err := r.RowsAffected()
	if err == nil && n != 1 {
		return fmt.Errorf("lease lost")
	}
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE job_details SET receipt=NULL WHERE id=?`, l.ID); err != nil {
		return err
	}
	return tx.Commit()
}
