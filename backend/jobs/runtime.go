package jobs

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Candidate identifies a concrete provider result, never a model-invented download.
type Candidate struct {
	ID          string          `json:"id"`
	Provider    string          `json:"provider"`
	Title       string          `json:"title"`
	Author      string          `json:"author"`
	Language    string          `json:"language,omitempty"`
	Narrator    string          `json:"narrator,omitempty"`
	Edition     string          `json:"edition,omitempty"`
	Format      string          `json:"format"`
	Kind        string          `json:"kind,omitempty"`
	Extension   string          `json:"extension"`
	Volume      string          `json:"volume,omitempty"`
	Cover       string          `json:"cover,omitempty"`
	Description string          `json:"description,omitempty"`
	Seeders     *int            `json:"seeders,omitempty"`
	URL         string          `json:"-"`
	Extra       json.RawMessage `json:"-"`
}
type Presence struct {
	Service string `json:"service"`
	State   string `json:"state"`
	URL     string `json:"url,omitempty"`
	ItemID  string `json:"item_id,omitempty"`
}
type Receipt struct {
	ID            string         `json:"id"`
	Title         string         `json:"title,omitempty"`
	Author        string         `json:"author,omitempty"`
	Constraints   Constraints    `json:"constraints,omitempty"`
	State         string         `json:"state"`
	Paths         []string       `json:"paths,omitempty"`
	OriginalPaths []string       `json:"original_paths,omitempty"`
	Format        string         `json:"format,omitempty"`
	Volume        string         `json:"volume,omitempty"`
	SelectionDone bool           `json:"selection_done,omitempty"`
	OwnerTag      string         `json:"owner_tag,omitempty"`
	AccountID     int64          `json:"account_id,omitempty"`
	TransferLink  []byte         `json:"transfer_link,omitempty"`
	Completed     []RetainedFile `json:"completed,omitempty"`
	Ordered       bool           `json:"ordered,omitempty"`
}
type Source interface {
	Name() string
	Search(context.Context, Input, string) ([]Candidate, error)
	Acquire(context.Context, Candidate, string) (Receipt, error)
	Reconcile(context.Context, Receipt) (Receipt, error)
}

// PreparedSource supplies durable remote identity before any consuming request.
type PreparedSource interface {
	Prepare(context.Context, Candidate, string) (Receipt, error)
}

// SafeFailure means the provider established that no external effect occurred.
// Transport errors after submission must never be wrapped in SafeFailure.
type SafeFailure struct{ Err error }

var ErrQuotaExhausted = errors.New("source confirms allowance exhausted")

func (e SafeFailure) Error() string { return e.Err.Error() }
func (e SafeFailure) Unwrap() error { return e.Err }

type RouteValidator interface {
	ValidateRoute(context.Context, Input, string, string) error
}
type Library interface {
	Presence(context.Context, Input, string) ([]Presence, error)
	Import(context.Context, Input, string, []string, string) ([]Presence, error)
}
type Account struct {
	ID        int64  `json:"id"`
	Email     string `json:"email"`
	Remaining *int   `json:"remaining"`
	Reset     *int64 `json:"reset"`
	Checked   int64  `json:"checked"`
	Healthy   bool   `json:"healthy"`
}
type AccountValidator func(context.Context, string, string) (*int, *int64, error)
type Runtime struct {
	Store              *Store
	Sources            []Source
	Library            Library
	Staging            string
	Enabled            bool
	PublicURL          string
	GatewayKey         string
	EncryptionKey      []byte
	InstanceID         string
	ValidateAccount    AccountValidator
	ValidateNewAccount AccountValidator
	SearchCatalog      func(context.Context, string) ([]Candidate, error)
	Enrich             func(context.Context, Input) (Candidate, error)
	ExtractPhoto       func(context.Context, []byte, string) ([]Input, error)
	DecideCandidate    func(context.Context, Input, []Candidate) int
	ConvertAudio       func(context.Context, []string, string, string, string) (string, error)
	VerifyLocal        func(context.Context, string, []string) ([]string, error)
	VerifyArtifact     func(context.Context, Input, Receipt) error
	ModelStatus        func() map[string]string
	services           map[string]string
	mu                 sync.Mutex
}

func (r *Runtime) SetService(name, state string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.services == nil {
		r.services = map[string]string{}
	}
	r.services[name] = state
}
func (r *Runtime) Services() map[string]string {
	r.mu.Lock()
	out := map[string]string{}
	for k, v := range r.services {
		out[k] = v
	}
	r.mu.Unlock()
	if r.ModelStatus != nil {
		for k, v := range r.ModelStatus() {
			out[k] = v
		}
	}
	return out
}

func randomToken() string {
	b := make([]byte, 32)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
func tokenHash(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }
func (s *Store) migrateRuntime() error {
	var version int
	if e := s.db.QueryRow(`SELECT COALESCE(max(version),0) FROM job_schema`).Scan(&version); e != nil {
		return e
	}
	if version > 2 {
		return errors.New("database schema is newer than this binary")
	}
	_, e := s.db.Exec(`
CREATE TABLE IF NOT EXISTS job_details (id INTEGER PRIMARY KEY REFERENCES representations(id), candidate BLOB, receipt BLOB, error TEXT NOT NULL DEFAULT '', presence BLOB, updated INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS accounts (id INTEGER PRIMARY KEY,email TEXT NOT NULL UNIQUE,secret BLOB NOT NULL,remaining INTEGER,reset INTEGER,checked INTEGER NOT NULL DEFAULT 0,healthy INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS reservations (job_id INTEGER PRIMARY KEY REFERENCES representations(id),account_id INTEGER NOT NULL REFERENCES accounts(id),state TEXT NOT NULL DEFAULT 'reserved');
CREATE TABLE IF NOT EXISTS grants (hash TEXT PRIMARY KEY,owner TEXT NOT NULL,label TEXT NOT NULL,scopes TEXT NOT NULL,created INTEGER NOT NULL,revoked INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS grant_resources (hash TEXT PRIMARY KEY REFERENCES grants(hash),resource TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS bootstraps (hash TEXT PRIMARY KEY,owner TEXT NOT NULL,expires INTEGER NOT NULL,used INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS photo_units (hash TEXT PRIMARY KEY,owner TEXT NOT NULL,data BLOB,mime TEXT NOT NULL,state TEXT NOT NULL DEFAULT 'queued',result BLOB,error TEXT NOT NULL DEFAULT '',created INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS photo_options (hash TEXT PRIMARY KEY REFERENCES photo_units(hash),name TEXT NOT NULL,kind TEXT NOT NULL,formats TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS photo_constraints (hash TEXT PRIMARY KEY REFERENCES photo_units(hash),data BLOB NOT NULL);
CREATE TABLE IF NOT EXISTS routing (category TEXT PRIMARY KEY,format TEXT NOT NULL,library TEXT NOT NULL,library_id TEXT NOT NULL,path TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS request_categories (request_id INTEGER PRIMARY KEY REFERENCES requests(id),category TEXT NOT NULL REFERENCES routing(category));
CREATE TABLE IF NOT EXISTS representation_categories (job_id INTEGER PRIMARY KEY REFERENCES representations(id),category TEXT NOT NULL REFERENCES routing(category));
CREATE TABLE IF NOT EXISTS failed_sources (job_id INTEGER NOT NULL REFERENCES representations(id),provider TEXT NOT NULL,until INTEGER NOT NULL,PRIMARY KEY(job_id,provider));
CREATE TABLE IF NOT EXISTS failed_candidates (job_id INTEGER NOT NULL REFERENCES representations(id),provider TEXT NOT NULL,candidate_id TEXT NOT NULL,until INTEGER NOT NULL,PRIMARY KEY(job_id,provider,candidate_id));
CREATE TABLE IF NOT EXISTS provider_backoff (provider TEXT PRIMARY KEY,until INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS retained_files (job_id INTEGER NOT NULL REFERENCES representations(id),identity TEXT NOT NULL,path TEXT NOT NULL,size INTEGER NOT NULL,PRIMARY KEY(job_id,identity));
CREATE TABLE IF NOT EXISTS candidate_choices (job_id INTEGER PRIMARY KEY REFERENCES representations(id),candidate BLOB NOT NULL);
CREATE TABLE IF NOT EXISTS candidate_options (job_id INTEGER NOT NULL REFERENCES representations(id),key TEXT NOT NULL,data BLOB NOT NULL,expires INTEGER NOT NULL,PRIMARY KEY(job_id,key));
CREATE TABLE IF NOT EXISTS intent_history (id INTEGER PRIMARY KEY,representation_id INTEGER NOT NULL,provider TEXT NOT NULL,candidate_id TEXT NOT NULL,receipt BLOB,state TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS work_aliases (alias TEXT PRIMARY KEY,request_id INTEGER NOT NULL REFERENCES requests(id));
CREATE TABLE IF NOT EXISTS merged_requests (id INTEGER PRIMARY KEY REFERENCES requests(id),canonical INTEGER NOT NULL REFERENCES requests(id));
CREATE TABLE IF NOT EXISTS linked_jobs (id INTEGER PRIMARY KEY REFERENCES representations(id),canonical INTEGER NOT NULL REFERENCES representations(id));
CREATE TABLE IF NOT EXISTS job_bumps (job_id INTEGER PRIMARY KEY REFERENCES representations(id),rank INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS conversions (job_id INTEGER PRIMARY KEY REFERENCES representations(id),state TEXT NOT NULL,error TEXT NOT NULL DEFAULT '',output TEXT);
CREATE TABLE IF NOT EXISTS photo_retries (hash TEXT PRIMARY KEY REFERENCES photo_units(hash),attempts INTEGER NOT NULL DEFAULT 0,next_at INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS photo_leases (hash TEXT PRIMARY KEY REFERENCES photo_units(hash),until INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS request_metadata (request_id INTEGER PRIMARY KEY REFERENCES requests(id),cover TEXT NOT NULL,description TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS request_constraints (request_id INTEGER PRIMARY KEY REFERENCES requests(id),data BLOB NOT NULL);
CREATE TABLE IF NOT EXISTS metadata_retries (request_id INTEGER PRIMARY KEY REFERENCES requests(id),eligible_at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS placements (destination TEXT PRIMARY KEY,job_id INTEGER NOT NULL REFERENCES representations(id),source TEXT NOT NULL,digest TEXT NOT NULL,pending_digest TEXT);
CREATE TABLE IF NOT EXISTS correction_requests (job_id INTEGER PRIMARY KEY REFERENCES representations(id),active INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS library_refresh (library_key TEXT PRIMARY KEY,dirty INTEGER NOT NULL,requested_at INTEGER NOT NULL DEFAULT 0,lease_until INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS library_refresh_claims (library_key TEXT PRIMARY KEY REFERENCES library_refresh(library_key),token TEXT NOT NULL,generation INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS runtime_settings (key TEXT PRIMARY KEY,value TEXT NOT NULL);
`)
	if e != nil {
		return e
	}
	if version < 2 {
		tx, e := s.db.Begin()
		if e != nil {
			return e
		}
		defer tx.Rollback()
		rows, e := tx.Query(`SELECT id,title,author,catalog_id,volume,kind FROM requests WHERE id NOT IN (SELECT id FROM merged_requests)`)
		if e != nil {
			return e
		}
		type entry struct {
			id int64
			in Input
		}
		entries := []entry{}
		for rows.Next() {
			var v entry
			if e = rows.Scan(&v.id, &v.in.Title, &v.in.Author, &v.in.CatalogID, &v.in.Volume, &v.in.Kind); e != nil {
				rows.Close()
				return e
			}
			entries = append(entries, v)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		for _, v := range entries {
			aliases := []string{identity(v.in)}
			if v.in.CatalogID != "" {
				aliases = append(aliases, "catalog:"+v.in.CatalogID+":"+normalized(v.in.Volume))
			}
			for _, a := range aliases {
				if _, e = tx.Exec(`INSERT OR IGNORE INTO work_aliases VALUES(?,?)`, a, v.id); e != nil {
					return e
				}
			}
		}
		if _, e = tx.Exec(`INSERT OR IGNORE INTO job_schema VALUES(2)`); e != nil {
			return e
		}
		return tx.Commit()
	}
	return nil
}
func NewRuntime(s *Store, key []byte) (*Runtime, error) {
	if len(key) != 32 {
		return nil, errors.New("encryption key must be 32 bytes")
	}
	if e := s.migrateRuntime(); e != nil {
		return nil, e
	}
	r := &Runtime{Store: s, EncryptionKey: key}
	var encrypted []byte
	if e := s.db.QueryRow(`SELECT secret FROM accounts LIMIT 1`).Scan(&encrypted); e == nil {
		if _, e = r.unseal(encrypted); e != nil {
			return nil, errors.New("account encryption key does not match persisted data")
		}
	} else if !errors.Is(e, sql.ErrNoRows) {
		return nil, e
	}
	if _, e := s.db.Exec(`INSERT OR IGNORE INTO runtime_settings VALUES('instance_id',?)`, randomToken()); e != nil {
		return nil, e
	}
	if e := s.db.QueryRow(`SELECT value FROM runtime_settings WHERE key='instance_id'`).Scan(&r.InstanceID); e != nil {
		return nil, e
	}
	return r, nil
}
func (r *Runtime) seal(plain []byte) ([]byte, error) {
	b, e := aes.NewCipher(r.EncryptionKey)
	if e != nil {
		return nil, e
	}
	g, e := cipher.NewGCM(b)
	if e != nil {
		return nil, e
	}
	nonce := make([]byte, g.NonceSize())
	if _, e = rand.Read(nonce); e != nil {
		return nil, e
	}
	return g.Seal(nonce, nonce, plain, []byte("arrdio-accounts-v1")), nil
}
func (r *Runtime) unseal(data []byte) ([]byte, error) {
	b, e := aes.NewCipher(r.EncryptionKey)
	if e != nil {
		return nil, e
	}
	g, e := cipher.NewGCM(b)
	if e != nil {
		return nil, e
	}
	if len(data) < g.NonceSize() {
		return nil, ErrInvalid
	}
	return g.Open(nil, data[:g.NonceSize()], data[g.NonceSize():], []byte("arrdio-accounts-v1"))
}
func (r *Runtime) AddAccount(ctx context.Context, email, password string) error {
	email = strings.ToLower(strings.TrimSpace(email))
	if !strings.Contains(email, "@") || len(email) > 254 || password == "" || len(password) > 1024 {
		return ErrInvalid
	}
	validator := r.ValidateNewAccount
	if validator == nil {
		validator = r.ValidateAccount
	}
	if validator == nil {
		return errors.New("account validation not configured")
	}
	remaining, reset, e := validator(ctx, email, password)
	if e != nil {
		return errors.New("account could not be validated")
	}
	secret, e := r.seal([]byte(password))
	if e != nil {
		return e
	}
	_, e = r.Store.db.ExecContext(ctx, `INSERT OR IGNORE INTO accounts(email,secret,remaining,reset,checked,healthy) VALUES(?,?,?,?,?,1)`, email, secret, remaining, reset, time.Now().Unix())
	return e
}
func (r *Runtime) Accounts(ctx context.Context) ([]Account, error) {
	rows, e := r.Store.db.QueryContext(ctx, `SELECT id,email,remaining,reset,checked,healthy FROM accounts ORDER BY id`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Account{}
	for rows.Next() {
		var a Account
		if e = rows.Scan(&a.ID, &a.Email, &a.Remaining, &a.Reset, &a.Checked, &a.Healthy); e != nil {
			return nil, e
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
func (r *Runtime) AccountCredentials(ctx context.Context, id int64) (string, string, error) {
	var email string
	var data []byte
	e := r.Store.db.QueryRowContext(ctx, `SELECT email,secret FROM accounts WHERE id=?`, id).Scan(&email, &data)
	if e != nil {
		return "", "", e
	}
	p, e := r.unseal(data)
	return email, string(p), e
}
func (r *Runtime) Quota(ctx context.Context) (map[string]any, error) {
	accounts, e := r.Accounts(ctx)
	if e != nil {
		return nil, e
	}
	total := 0
	known := len(accounts) > 0
	usable := 0
	var reset *int64
	for _, a := range accounts {
		if !a.Healthy {
			continue
		}
		if a.Remaining == nil || time.Now().Unix()-a.Checked > 600 {
			known = false
			continue
		}
		usable++
		total += *a.Remaining
		if a.Reset != nil && *a.Reset > time.Now().Unix() && (reset == nil || *a.Reset < *reset) {
			reset = a.Reset
		}
	}
	var pending int
	if usable == 0 {
		known = false
	}
	e = r.Store.db.QueryRowContext(ctx, `SELECT count(*) FROM reservations v JOIN accounts a ON a.id=v.account_id WHERE v.state='reserved' AND a.healthy=1 AND a.remaining IS NOT NULL AND a.checked>?`, time.Now().Add(-10*time.Minute).Unix()).Scan(&pending)
	if e != nil {
		return nil, e
	}
	total -= pending
	if total < 0 {
		total = 0
	}
	out := map[string]any{"remaining": nil, "reset": nil, "state": "checking"}
	if known {
		out["remaining"] = total
		out["state"] = "known"
		if total < 5 {
			out["reset"] = reset
		}
	}
	return out, nil
}
func (r *Runtime) RefreshAccounts(ctx context.Context) {
	if r.ValidateAccount == nil {
		return
	}
	accounts, e := r.Accounts(ctx)
	if e != nil {
		return
	}
	for _, a := range accounts {
		if time.Now().Unix()-a.Checked < 60 {
			continue
		}
		email, pw, e := r.AccountCredentials(ctx, a.ID)
		if e != nil {
			continue
		}
		n, reset, e := r.ValidateAccount(ctx, email, pw)
		healthy := e == nil
		r.Store.db.ExecContext(ctx, `UPDATE accounts SET remaining=?,reset=?,checked=?,healthy=? WHERE id=?`, n, reset, time.Now().Unix(), healthy, a.ID)
	}
}
func (r *Runtime) Reserve(ctx context.Context, job int64) (int64, error) {
	r.recoverReservations(ctx)
	tx, e := r.Store.db.BeginTx(ctx, nil)
	if e != nil {
		return 0, e
	}
	defer tx.Rollback()
	var id int64
	e = tx.QueryRowContext(ctx, `SELECT account_id FROM reservations WHERE job_id=?`, job).Scan(&id)
	if e == nil {
		return id, nil
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return 0, e
	}
	e = tx.QueryRowContext(ctx, `SELECT a.id FROM accounts a WHERE healthy=1 AND checked>? AND remaining>(SELECT count(*) FROM reservations WHERE account_id=a.id AND state='reserved') AND NOT EXISTS(SELECT 1 FROM reservations WHERE account_id=a.id AND state='reserved') ORDER BY a.id LIMIT 1`, time.Now().Add(-10*time.Minute).Unix()).Scan(&id)
	if e != nil {
		return 0, e
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO reservations(job_id,account_id) VALUES(?,?)`, job, id); e != nil {
		return 0, e
	}
	return id, tx.Commit()
}
func (r *Runtime) finishReservation(ctx context.Context, job int64) {
	r.Store.db.ExecContext(ctx, `UPDATE accounts SET checked=0 WHERE id=(SELECT account_id FROM reservations WHERE job_id=?)`, job)
	r.Store.db.ExecContext(ctx, `UPDATE reservations SET state='settled' WHERE job_id=?`, job)
}
func (r *Runtime) recoverReservations(ctx context.Context) {
	// Before intent there cannot have been a consuming call. After intent, refresh
	// provider truth before reusing the account; the ambiguous job stays fenced.
	r.Store.db.ExecContext(ctx, `DELETE FROM reservations WHERE state='reserved' AND NOT EXISTS(SELECT 1 FROM transfer_intents WHERE representation_id=job_id AND state='unresolved') AND EXISTS(SELECT 1 FROM representations j WHERE j.id=job_id AND (j.state='canceled' OR j.lease_until IS NULL OR j.lease_until<=?))`, time.Now().Unix())
	tx, e := r.Store.db.BeginTx(ctx, nil)
	if e != nil {
		return
	}
	defer tx.Rollback()
	_, e = tx.ExecContext(ctx, `UPDATE accounts SET checked=0 WHERE id IN (SELECT account_id FROM reservations v JOIN representations j ON j.id=v.job_id WHERE v.state='reserved' AND EXISTS(SELECT 1 FROM transfer_intents WHERE representation_id=j.id AND state='unresolved') AND (j.state='canceled' OR j.lease_until IS NULL OR j.lease_until<=?))`, time.Now().Unix())
	if e != nil {
		return
	}
	_, e = tx.ExecContext(ctx, `UPDATE reservations SET state='uncertain' WHERE state='reserved' AND EXISTS(SELECT 1 FROM transfer_intents WHERE representation_id=job_id AND state='unresolved') AND EXISTS(SELECT 1 FROM representations j WHERE j.id=job_id AND (j.state='canceled' OR j.lease_until IS NULL OR j.lease_until<=?))`, time.Now().Unix())
	if e == nil {
		tx.Commit()
	}
}
func (s *Store) owns(ctx context.Context, l Lease) bool {
	var n int
	e := s.db.QueryRowContext(ctx, `SELECT 1 FROM representations WHERE id=? AND lease_token=? AND lease_until>? AND state!='canceled'`, l.ID, l.Token, time.Now().Unix()).Scan(&n)
	return e == nil
}
func (s *Store) phase(ctx context.Context, l Lease, state string) bool {
	result, e := s.db.ExecContext(ctx, `UPDATE representations SET state=? WHERE id=? AND lease_token=? AND lease_until>? AND state!='canceled'`, state, l.ID, l.Token, time.Now().Unix())
	if e != nil {
		return false
	}
	n, _ := result.RowsAffected()
	if n == 1 {
		// A new phase supersedes the previous attempt's error; showing it beside
		// "importing" or "converting" read as a failure while work progressed.
		s.db.ExecContext(ctx, `UPDATE job_details SET error='' WHERE id=?`, l.ID)
	}
	return n == 1
}
func (r *Runtime) leaseContext(parent context.Context, l Lease) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(parent, 6*time.Hour)
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				result, e := r.Store.db.ExecContext(ctx, `UPDATE representations SET lease_until=? WHERE id=? AND lease_token=? AND lease_until>? AND state!='canceled'`, time.Now().Add(10*time.Minute).Unix(), l.ID, l.Token, time.Now().Unix())
				if e != nil {
					cancel()
					return
				}
				n, _ := result.RowsAffected()
				if n != 1 {
					cancel()
					return
				}
			}
		}
	}()
	return ctx, cancel
}
func (r *Runtime) category(ctx context.Context, l Lease) string {
	var category string
	r.Store.db.QueryRowContext(ctx, `SELECT category FROM representation_categories WHERE job_id=?`, l.ID).Scan(&category)
	return category
}
func (r *Runtime) reject(ctx context.Context, l Lease, provider string) {
	tx, e := r.Store.db.BeginTx(ctx, nil)
	if e != nil {
		return
	}
	defer tx.Rollback()
	if _, e = tx.ExecContext(ctx, `UPDATE transfer_intents SET state='rejected' WHERE representation_id=?`, l.ID); e != nil {
		return
	}
	if _, e = tx.ExecContext(ctx, `DELETE FROM reservations WHERE job_id=?`, l.ID); e != nil {
		return
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO failed_sources VALUES(?,?,?) ON CONFLICT(job_id,provider) DO UPDATE SET until=excluded.until`, l.ID, provider, time.Now().Add(15*time.Minute).Unix()); e != nil {
		return
	}
	tx.Commit()
}

func (s *Store) input(ctx context.Context, id int64) (Input, error) {
	id = s.canonicalID(ctx, id)
	var in Input
	e := s.db.QueryRowContext(ctx, `SELECT title,author,catalog_id,volume,kind FROM requests WHERE id=?`, id).Scan(&in.Title, &in.Author, &in.CatalogID, &in.Volume, &in.Kind)
	if e == nil {
		constraints, err := s.constraints(ctx, id)
		if err == nil {
			in.Constraints = constraints
		} else if !errors.Is(err, sql.ErrNoRows) {
			return in, err
		}
	}
	return in, e
}
func (s *Store) transition(ctx context.Context, l Lease, state, message string, receipt *Receipt, presence []Presence) error {
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	result, e := tx.ExecContext(ctx, `UPDATE representations SET state=?,eligible_at=?,lease_token=NULL,lease_until=NULL WHERE id=? AND lease_token=? AND state!='canceled'`, state, time.Now().Add(time.Minute).Unix(), l.ID, l.Token)
	if e != nil {
		return e
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return errors.New("lease lost")
	}
	rec, _ := json.Marshal(receipt)
	p, _ := json.Marshal(presence)
	_, e = tx.ExecContext(ctx, `INSERT INTO job_details(id,error,receipt,presence,updated) VALUES(?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET error=excluded.error,receipt=COALESCE(NULLIF(excluded.receipt,'null'),job_details.receipt),presence=COALESCE(NULLIF(excluded.presence,'null'),job_details.presence),updated=excluded.updated`, l.ID, message, string(rec), string(p), time.Now().Unix())
	if e != nil {
		return e
	}
	if state == "available" {
		if _, e = tx.ExecContext(ctx, `UPDATE transfer_intents SET state='completed',receipt=? WHERE representation_id=?`, string(rec), l.ID); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, `UPDATE correction_requests SET active=0 WHERE job_id=?`, l.ID); e != nil {
			return e
		}
	}
	return tx.Commit()
}
func (s *Store) saveCandidate(ctx context.Context, l Lease, c Candidate) error {
	// Keep the private URL and provider payload in the database, never client JSON.
	data, e := json.Marshal(struct {
		Candidate Candidate
		URL       string
		Extra     json.RawMessage
	}{c, c.URL, c.Extra})
	if e != nil {
		return e
	}
	_, e = s.db.ExecContext(ctx, `INSERT INTO job_details(id,candidate) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET candidate=excluded.candidate`, l.ID, data)
	return e
}
func (r *Runtime) Run(ctx context.Context) {
	var wg sync.WaitGroup
	// One worker per lane bounds calls; source accounts have independent reservations.
	for _, format := range []string{"ebook", "audio", "manga"} {
		wg.Add(1)
		go func(f string) {
			defer wg.Done()
			timer := time.NewTicker(2 * time.Second)
			defer timer.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-timer.C:
					if r.Enabled {
						r.step(ctx, f)
					}
				}
			}
		}(format)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		timer := time.NewTicker(15 * time.Second)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
				r.recoverReservations(ctx)
				r.RefreshAccounts(ctx)
			}
		}
	}()
	for _, operation := range []func(context.Context){r.processPhoto, r.enrichPending} {
		wg.Add(1)
		go func(run func(context.Context)) {
			defer wg.Done()
			ticker := time.NewTicker(2 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					run(ctx)
				}
			}
		}(operation)
	}
	<-ctx.Done()
	wg.Wait()
}
func (r *Runtime) enrichPending(ctx context.Context) {
	if r.Enrich == nil {
		return
	}
	var id int64
	e := r.Store.db.QueryRowContext(ctx, `SELECT r.id FROM requests r LEFT JOIN request_metadata m ON m.request_id=r.id LEFT JOIN metadata_retries retry ON retry.request_id=r.id WHERE m.request_id IS NULL AND COALESCE(retry.eligible_at,0)<=? AND r.id NOT IN (SELECT id FROM merged_requests) ORDER BY r.rank DESC LIMIT 1`, time.Now().Unix()).Scan(&id)
	if e != nil {
		return
	}
	in, e := r.Store.input(ctx, id)
	if e != nil {
		return
	}
	metadataCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	meta, e := r.Enrich(metadataCtx, in)
	if e != nil {
		r.Store.db.ExecContext(ctx, `INSERT INTO metadata_retries VALUES(?,?) ON CONFLICT(request_id) DO UPDATE SET eligible_at=excluded.eligible_at`, id, time.Now().Add(10*time.Minute).Unix())
		return
	}
	r.Store.db.ExecContext(ctx, `INSERT OR IGNORE INTO request_metadata VALUES(?,?,?)`, id, meta.Cover, meta.Description)
}
func (r *Runtime) step(ctx context.Context, format string) {
	r.resume(ctx, format)
	// Safe waiting states can return to dispatch; ambiguous intents cannot.
	r.Store.db.ExecContext(ctx, `UPDATE representations SET state='queued' WHERE format=? AND state IN ('waiting_allowance','waiting_library','missing','retryable_failure','waiting_provider') AND eligible_at<=? AND NOT EXISTS(SELECT 1 FROM transfer_intents WHERE representation_id=representations.id AND state='unresolved')`, format, time.Now().Unix())
	l, e := r.Store.Claim(ctx, format, randomToken(), time.Now(), 10*time.Minute)
	if e != nil {
		return
	}
	ctx, cancel := r.leaseContext(ctx, l)
	defer cancel()
	in, e := r.Store.input(ctx, l.RequestID)
	if e != nil {
		return
	}
	if r.Enrich != nil {
		var exists int
		if r.Store.db.QueryRowContext(ctx, `SELECT 1 FROM request_metadata WHERE request_id=?`, l.RequestID).Scan(&exists) != nil {
			metadataCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			meta, e := r.Enrich(metadataCtx, in)
			cancel()
			if e == nil {
				r.Store.db.ExecContext(ctx, `INSERT OR IGNORE INTO request_metadata VALUES(?,?,?)`, l.RequestID, meta.Cover, meta.Description)
			}
		}
	}
	end := func(state, message string, rec *Receipt, p []Presence) {
		r.Store.transition(ctx, l, state, message, rec, p)
	}
	category := r.category(ctx, l)
	var explicitChoice int
	r.Store.db.QueryRowContext(ctx, `SELECT count(*) FROM candidate_choices WHERE job_id=?`, l.ID).Scan(&explicitChoice)
	if v, ok := r.Library.(RouteValidator); ok {
		if e := v.ValidateRoute(ctx, in, format, category); e != nil {
			end("waiting_library", "No compatible writable destination route", nil, nil)
			return
		}
	}
	if r.Library == nil {
		end("waiting_library", "Library connection is not configured", nil, nil)
		return
	}
	presence, e := r.Library.Presence(ctx, in, format)
	if e != nil {
		end("waiting_library", "Library availability could not be confirmed", nil, presence)
		return
	}
	for _, p := range presence {
		if p.State == "available" && explicitChoice == 0 {
			end("available", "", nil, presence)
			return
		}
		if p.State == "unknown" {
			end("waiting_library", "Library availability is unknown", nil, presence)
			return
		}
	}
	var chosen *Candidate
	var source Source
	deferred := false
	var selected []byte
	if r.Store.db.QueryRowContext(ctx, `SELECT candidate FROM candidate_choices WHERE job_id=?`, l.ID).Scan(&selected) == nil {
		var saved struct {
			Candidate Candidate
			URL       string
			Extra     json.RawMessage
		}
		if json.Unmarshal(selected, &saved) == nil {
			saved.Candidate.URL = saved.URL
			saved.Candidate.Extra = saved.Extra
			chosen = &saved.Candidate
			for _, s := range r.Sources {
				if s.Name() == chosen.Provider {
					source = s
					break
				}
			}
		}
	}
	for _, s := range r.Sources {
		if chosen != nil && source != nil {
			break
		}
		if r.sourceDeferred(ctx, s.Name()) {
			deferred = true
			continue
		}
		var excluded int
		if r.Store.db.QueryRowContext(ctx, `SELECT 1 FROM failed_sources WHERE job_id=? AND provider=? AND until>?`, l.ID, s.Name(), time.Now().Unix()).Scan(&excluded) == nil {
			continue
		}
		results, err := s.Search(ctx, in, format)
		if err != nil {
			deferred = true
			r.SetService(s.Name(), "unavailable")
			r.deferSource(ctx, s.Name(), time.Minute)
			continue
		}
		if len(results) > 0 {
			r.SetService(s.Name(), "ready")
		}
		eligible := results[:0]
		for _, candidate := range results {
			if !in.Allows(candidate, format) {
				continue
			}
			var excluded int
			if r.Store.db.QueryRowContext(ctx, `SELECT 1 FROM failed_candidates WHERE job_id=? AND provider=? AND candidate_id=? AND until>?`, l.ID, s.Name(), candidate.ID, time.Now().Unix()).Scan(&excluded) != nil {
				eligible = append(eligible, candidate)
			}
		}
		results = eligible
		if len(results) > 0 {
			index := 0
			if r.DecideCandidate != nil {
				index = r.DecideCandidate(ctx, in, results)
			}
			if index < 0 || index >= len(results) {
				index = 0
			}
			chosen = &results[index]
			source = s
			break
		}
	}
	if chosen == nil || source == nil {
		if deferred {
			end("waiting_provider", "Provider recovery is in progress; no new transfer requested", nil, presence)
			return
		}
		end("missing", "No suitable source result; automatic retry scheduled", nil, presence)
		return
	}
	if !in.Allows(*chosen, format) {
		end("missing", "No candidate satisfies the explicit format requirements", nil, presence)
		return
	}
	if r.sourceDeferred(ctx, source.Name()) {
		end("waiting_provider", "Provider recovery is in progress; no new transfer requested", nil, presence)
		return
	}
	if keep, e := r.Store.Canonicalize(ctx, l, in, *chosen); e != nil {
		if errors.Is(e, ErrMergePending) {
			end("waiting_provider", "Related requests have active transfers; identity consolidation will retry after recovery", nil, nil)
			return
		}
		end("retryable_failure", "Work identity could not be reconciled", nil, nil)
		return
	} else if !keep {
		return
	}
	if e = r.Store.saveCandidate(ctx, l, *chosen); e != nil {
		end("retryable_failure", "Could not save candidate", nil, nil)
		return
	}
	if chosen.Provider == "zlibrary" {
		account, e := r.Reserve(ctx, l.ID)
		if e != nil {
			end("waiting_allowance", "Waiting for usable household allowance", nil, presence)
			return
		}
		chosen.Extra = json.RawMessage(fmt.Sprintf(`{"account_id":%d}`, account))
	}
	destination := r.candidateDirectory(l.ID, *chosen)
	if e = os.MkdirAll(destination, 0700); e != nil {
		r.Store.db.ExecContext(ctx, `DELETE FROM reservations WHERE job_id=? AND state='reserved' AND NOT EXISTS(SELECT 1 FROM transfer_intents WHERE representation_id=?)`, l.ID, l.ID)
		end("retryable_failure", "Staging is unavailable", nil, nil)
		return
	}
	if e = r.Store.RecordIntent(ctx, l, source.Name(), chosen.ID, time.Now()); e != nil {
		r.Store.db.ExecContext(ctx, `DELETE FROM reservations WHERE job_id=? AND NOT EXISTS(SELECT 1 FROM transfer_intents WHERE representation_id=? AND state='unresolved')`, l.ID, l.ID)
		end("retryable_failure", "Transfer requires reconciliation", nil, nil)
		return
	}
	// Persist complete reconciliation identity before calling the remote service.
	receipt := Receipt{ID: chosen.ID, State: "unresolved"}
	if preparer, ok := source.(PreparedSource); ok {
		receipt, e = preparer.Prepare(ctx, *chosen, destination)
		if e != nil {
			r.reject(ctx, l, source.Name())
			end("waiting_provider", "Provider rejected preparation; trying alternatives", nil, nil)
			return
		}
	}
	if e = r.Store.saveCandidate(ctx, l, *chosen); e != nil {
		return
	}
	prepared, _ := json.Marshal(receipt)
	if _, e = r.Store.db.ExecContext(ctx, `UPDATE job_details SET receipt=? WHERE id=?`, string(prepared), l.ID); e != nil {
		return
	}
	if !r.Store.owns(ctx, l) {
		r.reject(ctx, l, source.Name())
		return
	}
	if !r.Store.phase(ctx, l, "transferring") {
		return
	}
	ctx = WithTransferCheckpoint(ctx, func(receipt Receipt) error {
		checkpointCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		data, e := json.Marshal(receipt)
		if e != nil {
			return e
		}
		_, e = r.Store.db.ExecContext(checkpointCtx, `UPDATE job_details SET receipt=? WHERE id=?`, string(data), l.ID)
		return e
	})
	ctx = context.WithValue(ctx, retainedFilesKey{}, r.retainedFiles(ctx, l.ID))
	acquired, acquireErr := source.Acquire(ctx, *chosen, destination)
	if acquired.ID != "" {
		receipt = acquired
	}
	receipt.Constraints = in.Constraints
	e = acquireErr
	data, _ := json.Marshal(receipt)
	r.Store.db.ExecContext(ctx, `UPDATE job_details SET receipt=? WHERE id=?`, string(data), l.ID)
	var safe SafeFailure
	if errors.As(e, &safe) {
		if errors.Is(e, ErrQuotaExhausted) {
			r.Store.db.ExecContext(ctx, `UPDATE accounts SET remaining=0,checked=? WHERE id=(SELECT account_id FROM reservations WHERE job_id=?)`, time.Now().Unix(), l.ID)
			r.reject(ctx, l, source.Name())
			r.Store.db.ExecContext(ctx, `DELETE FROM failed_sources WHERE job_id=? AND provider=?`, l.ID, source.Name())
			end("waiting_allowance", "Account allowance exhausted; household rollover scheduled", &receipt, nil)
			r.Store.db.ExecContext(ctx, `UPDATE representations SET eligible_at=0 WHERE id=?`, l.ID)
			return
		}
		r.deferSource(ctx, source.Name(), time.Minute)
		r.reject(ctx, l, source.Name())
		end("waiting_provider", "Provider rejected the request before transfer; trying alternatives", &receipt, nil)
		return
	}
	if chosen.Provider == "zlibrary" {
		r.finishReservation(ctx, l.ID)
	}
	if e != nil {
		r.deferSource(ctx, source.Name(), 5*time.Minute)
		var final FinalTransferFailure
		if errors.As(e, &final) {
			r.Store.db.ExecContext(ctx, `UPDATE transfer_intents SET state='failed' WHERE representation_id=?`, l.ID)
			end("permanent_failure", "Source returned an unusable or unavailable file; choose another result", &receipt, nil)
			return
		}
		end("retryable_failure", "Transfer outcome needs reconciliation; not automatically repeated", &receipt, nil)
		return
	}
	r.clearSourceBackoff(ctx, source.Name())
	if receipt.State != "verified" {
		state := "transferring"
		if receipt.State == "provider_wait" {
			state = "provider_wait"
		}
		end(state, "", &receipt, nil)
		return
	}
	if r.VerifyArtifact != nil {
		if verifyErr := r.VerifyArtifact(ctx, in, receipt); verifyErr != nil {
			r.Store.db.ExecContext(ctx, `UPDATE transfer_intents SET state='failed' WHERE representation_id=?`, l.ID)
			end("permanent_failure", "Artifact metadata contradicts or does not verify the requested identity or requirements; choose another result", &receipt, nil)
			return
		}
	}
	if !r.Store.owns(ctx, l) {
		return
	}
	// Import implementation validates bytes and performs server-owned routing.
	receipt = r.convert(ctx, l, in, receipt)
	if !requiredOutput(in, receipt) {
		end("importing", "Required container is not ready; original download retained", &receipt, nil)
		return
	}
	if !r.Store.owns(ctx, l) {
		return
	}
	if !r.Store.phase(ctx, l, "importing") {
		return
	}
	p, e := r.Library.Import(r.importContext(ctx, l), in, format, receipt.Paths, r.category(ctx, l))
	if e != nil {
		message := "Verified download retained; library import pending"
		if errors.Is(e, ErrPlacementConflict) {
			message = "Placement conflict: an unowned, modified, or seeded file was preserved"
		}
		end("importing", message, &receipt, p)
		return
	}
	state := "importing"
	for _, v := range p {
		if v.State == "available" {
			state = "available"
		}
	}
	end(state, "", &receipt, p)
}

// Resume polls existing transfers and imports; it never repeats Acquire or /file.
func (r *Runtime) resume(ctx context.Context, format string) {
	token := randomToken()
	var l Lease
	l.Token = token
	l.Format = format
	// Poll already-started work by next due check, so a few old waits cannot starve
	// thousands of newer transfer receipts. New acquisition dispatch remains LIFO.
	e := r.Store.db.QueryRowContext(ctx, `UPDATE representations SET lease_token=?,lease_until=? WHERE id=(SELECT j.id FROM representations j JOIN transfer_intents t ON t.representation_id=j.id WHERE j.format=? AND j.state IN ('transferring','provider_wait','importing','converting','verifying','retryable_failure','matching') AND j.eligible_at<=? AND (j.lease_until IS NULL OR j.lease_until<=?) ORDER BY j.eligible_at,j.id DESC LIMIT 1) RETURNING id,request_id`, token, time.Now().Add(10*time.Minute).Unix(), format, time.Now().Unix(), time.Now().Unix()).Scan(&l.ID, &l.RequestID)
	if e != nil {
		return
	}
	ctx, cancel := r.leaseContext(ctx, l)
	defer cancel()
	var provider string
	var raw []byte
	r.Store.db.QueryRowContext(ctx, `SELECT provider FROM transfer_intents WHERE representation_id=?`, l.ID).Scan(&provider)
	r.Store.db.QueryRowContext(ctx, `SELECT receipt FROM job_details WHERE id=?`, l.ID).Scan(&raw)
	var receipt Receipt
	json.Unmarshal(raw, &receipt)
	if provider == "zlibrary" && receipt.AccountID == 0 {
		r.Store.db.QueryRowContext(ctx, `SELECT account_id FROM reservations WHERE job_id=?`, l.ID).Scan(&receipt.AccountID)
	}
	if receipt.ID == "" {
		var saved struct {
			Candidate Candidate
			URL       string
			Extra     json.RawMessage
		}
		var data []byte
		r.Store.db.QueryRowContext(ctx, `SELECT candidate FROM job_details WHERE id=?`, l.ID).Scan(&data)
		if json.Unmarshal(data, &saved) == nil {
			saved.Candidate.URL = saved.URL
			saved.Candidate.Extra = saved.Extra
			for _, s := range r.Sources {
				if s.Name() == provider {
					if prepare, ok := s.(PreparedSource); ok {
						receipt, _ = prepare.Prepare(ctx, saved.Candidate, r.candidateDirectory(l.ID, saved.Candidate))
					}
				}
			}
		}
	}
	var source Source
	for _, s := range r.Sources {
		if s.Name() == provider {
			source = s
			break
		}
	}
	var next Receipt
	retained := false
	if receipt.State == "verified" && r.VerifyLocal != nil {
		if paths, verifyErr := r.VerifyLocal(ctx, format, receipt.Paths); verifyErr == nil {
			next = receipt
			next.Paths = paths
			retained = true
		}
	}
	if !retained && receipt.State == "verified" && r.VerifyLocal != nil && len(receipt.OriginalPaths) > 0 {
		if paths, verifyErr := r.VerifyLocal(ctx, format, receipt.OriginalPaths); verifyErr == nil {
			next = receipt
			next.Paths = paths
			retained = true
			r.Store.db.ExecContext(ctx, `UPDATE conversions SET state='pending',output=NULL WHERE job_id=?`, l.ID)
		}
	}
	if source == nil && !retained {
		r.Store.transition(ctx, l, "retryable_failure", "Provider unavailable for reconciliation", &receipt, nil)
		return
	}
	if !retained {
		next, e = source.Reconcile(ctx, receipt)
	}
	var retry RetryTransferFailure
	if errors.As(e, &retry) {
		r.failCandidate(ctx, l, provider, next)
		return
	}
	var final FinalTransferFailure
	if errors.As(e, &final) {
		r.finishReservation(ctx, l.ID)
		r.Store.db.ExecContext(ctx, `UPDATE transfer_intents SET state='failed' WHERE representation_id=?`, l.ID)
		r.Store.transition(ctx, l, "permanent_failure", "Source transfer is no longer usable; choose another result", &next, nil)
		return
	}
	if e != nil || next.State != "verified" {
		pending := errors.Is(e, ErrPayloadPending)
		if e != nil && !pending && !errors.Is(e, ErrUnresolvedTransfer) && !errors.Is(e, ErrInvalid) {
			r.deferSource(ctx, provider, 5*time.Minute)
		}
		state := "transferring"
		if next.State == "provider_wait" {
			state = "provider_wait"
		}
		msg := ""
		if e != nil || next.ID == "" {
			state = "retryable_failure"
			msg = "Transfer outcome unresolved; no repeated source charge"
			if pending {
				msg = "Download complete; waiting for its files to become readable"
			}
		}
		r.Store.transition(ctx, l, state, msg, &next, nil)
		return
	}
	if !retained {
		r.clearSourceBackoff(ctx, provider)
	}
	verifiedBytes, _ := json.Marshal(next)
	if _, e = r.Store.db.ExecContext(ctx, `UPDATE job_details SET receipt=? WHERE id=?`, string(verifiedBytes), l.ID); e != nil {
		return
	}
	in, e := r.Store.input(ctx, l.RequestID)
	if e != nil {
		return
	}
	if r.VerifyArtifact != nil {
		if verifyErr := r.VerifyArtifact(ctx, in, next); verifyErr != nil {
			r.Store.db.ExecContext(ctx, `UPDATE transfer_intents SET state='failed' WHERE representation_id=?`, l.ID)
			r.Store.transition(ctx, l, "permanent_failure", "Artifact metadata does not verify the requested identity or requirements; choose another result", &next, nil)
			return
		}
	}
	if r.Library == nil {
		r.Store.transition(ctx, l, "importing", "Library unavailable", &next, nil)
		return
	}
	if !r.Store.owns(ctx, l) {
		return
	}
	next = r.convert(ctx, l, in, next)
	if !requiredOutput(in, next) {
		r.Store.transition(ctx, l, "importing", "Required container is not ready; original download retained", &next, nil)
		return
	}
	if !r.Store.owns(ctx, l) {
		return
	}
	if !r.Store.phase(ctx, l, "importing") {
		return
	}
	presence, e := r.Library.Import(r.importContext(ctx, l), in, format, next.Paths, r.category(ctx, l))
	state := "importing"
	message := ""
	if e != nil {
		log.Printf("job %d: import pending: %v", l.ID, e)
		message = "Import pending; verified originals retained"
		if errors.Is(e, ErrPlacementConflict) {
			message = "Placement conflict: an unowned, modified, or seeded file was preserved"
		}
	}
	for _, p := range presence {
		if p.State == "available" {
			state = "available"
		}
	}
	r.Store.transition(ctx, l, state, message, &next, presence)
}
func requiredOutput(in Input, receipt Receipt) bool {
	container := strings.TrimPrefix(in.Constraints.Container, ".")
	if container == "" {
		return true
	}
	for _, path := range receipt.Paths {
		if strings.EqualFold(filepath.Ext(path), "."+container) {
			return true
		}
	}
	return false
}
func (r *Runtime) sourceDeferred(ctx context.Context, name string) bool {
	var n int
	return r.Store.db.QueryRowContext(ctx, `SELECT 1 FROM provider_backoff WHERE provider=? AND until>?`, name, time.Now().Unix()).Scan(&n) == nil
}
func (r *Runtime) deferSource(ctx context.Context, name string, delay time.Duration) {
	r.Store.db.ExecContext(ctx, `INSERT INTO provider_backoff VALUES(?,?) ON CONFLICT(provider) DO UPDATE SET until=MAX(until,excluded.until)`, name, time.Now().Add(delay).Unix())
	r.SetService(name, "unavailable; retry backoff active")
}
func (r *Runtime) clearSourceBackoff(ctx context.Context, name string) {
	r.Store.db.ExecContext(ctx, `DELETE FROM provider_backoff WHERE provider=?`, name)
	r.SetService(name, "ready")
}
func (r *Runtime) retainedFiles(ctx context.Context, job int64) []RetainedFile {
	rows, e := r.Store.db.QueryContext(ctx, `SELECT identity,path,size FROM retained_files WHERE job_id=?`, job)
	if e != nil {
		return nil
	}
	defer rows.Close()
	out := []RetainedFile{}
	for rows.Next() {
		var f RetainedFile
		if rows.Scan(&f.Identity, &f.Path, &f.Size) == nil {
			out = append(out, f)
		}
	}
	return out
}
func (r *Runtime) failCandidate(ctx context.Context, l Lease, provider string, receipt Receipt) {
	tx, e := r.Store.db.BeginTx(ctx, nil)
	if e != nil {
		return
	}
	defer tx.Rollback()
	var candidate string
	if tx.QueryRowContext(ctx, `SELECT candidate_id FROM transfer_intents WHERE representation_id=?`, l.ID).Scan(&candidate) != nil {
		return
	}
	data, _ := json.Marshal(receipt)
	if _, e = tx.ExecContext(ctx, `INSERT INTO intent_history(representation_id,provider,candidate_id,receipt,state) VALUES(?,?,?,?,'failed')`, l.ID, provider, candidate, string(data)); e != nil {
		return
	}
	if _, e = tx.ExecContext(ctx, `UPDATE transfer_intents SET state='rejected' WHERE representation_id=?`, l.ID); e != nil {
		return
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO failed_candidates VALUES(?,?,?,?) ON CONFLICT(job_id,provider,candidate_id) DO UPDATE SET until=excluded.until`, l.ID, provider, candidate, time.Now().Add(30*time.Minute).Unix()); e != nil {
		return
	}
	for _, f := range receipt.Completed {
		if _, e = tx.ExecContext(ctx, `INSERT INTO retained_files VALUES(?,?,?,?) ON CONFLICT(job_id,identity) DO UPDATE SET path=excluded.path,size=excluded.size`, l.ID, f.Identity, f.Path, f.Size); e != nil {
			return
		}
	}
	if _, e = tx.ExecContext(ctx, `DELETE FROM candidate_choices WHERE job_id=?`, l.ID); e != nil {
		return
	}
	if tx.Commit() != nil {
		return
	}
	r.Store.transition(ctx, l, "waiting_provider", "Peer transfer failed; completed files retained and another candidate will be tried", &receipt, nil)
}
func (r *Runtime) candidateDirectory(job int64, c Candidate) string {
	return filepath.Join(r.Staging, fmt.Sprint(job), tokenHash(c.Provider + ":" + c.ID)[:16])
}

func (r *Runtime) convert(ctx context.Context, l Lease, in Input, receipt Receipt) Receipt {
	if l.Format != "audio" || r.ConvertAudio == nil {
		return receipt
	}
	if container := strings.TrimPrefix(in.Constraints.Container, "."); container != "" && container != "m4b" {
		return receipt
	}
	if len(receipt.OriginalPaths) == 0 {
		receipt.OriginalPaths = append([]string(nil), receipt.Paths...)
	}
	var state, output string
	r.Store.db.QueryRowContext(ctx, `SELECT state,COALESCE(output,'') FROM conversions WHERE job_id=?`, l.ID).Scan(&state, &output)
	if state == "complete" {
		if _, e := os.Stat(output); e == nil {
			receipt.Paths = []string{output}
			return receipt
		}
	}
	if state == "failed" {
		return receipt
	}
	// Keep the lease through conversion. A crashed running stage is retried from
	// preserved originals; atomic converter output is reused on recovery.
	if !r.Store.phase(ctx, l, "converting") {
		return receipt
	}
	r.Store.db.ExecContext(ctx, `INSERT INTO conversions(job_id,state) VALUES(?,'running') ON CONFLICT(job_id) DO UPDATE SET state='running'`, l.ID)
	output = filepath.Join(r.Staging, fmt.Sprint(l.ID), "normalized.m4b")
	os.MkdirAll(filepath.Dir(output), 0700)
	result, e := r.ConvertAudio(WithOrderedTracks(ctx, receipt.Ordered), receipt.Paths, output, in.Title, in.Author)
	if e != nil {
		r.Store.db.ExecContext(ctx, `UPDATE conversions SET state='failed',error='Conversion failed; playable originals retained' WHERE job_id=?`, l.ID)
		return receipt
	}
	r.Store.db.ExecContext(ctx, `UPDATE conversions SET state='complete',error='',output=? WHERE job_id=?`, result, l.ID)
	receipt.Paths = []string{result}
	return receipt
}

func (r *Runtime) ConfigureRoute(ctx context.Context, category, format, service, id, path string) error {
	_, e := r.Store.db.ExecContext(ctx, `INSERT INTO routing(category,format,library,library_id,path) VALUES(?,?,?,?,?) ON CONFLICT(category) DO UPDATE SET format=excluded.format,library=excluded.library,library_id=excluded.library_id,path=excluded.path`, category, format, service, id, path)
	return e
}
