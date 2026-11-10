package jobs

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestSnapshotRestorePreservesQueueAccountsRoutesAndGrants(t *testing.T) {
	dir := t.TempDir()
	r := runtimeTest(t, filepath.Join(dir, "jobs.db"))
	seedAccount(t, r)
	batch := enqueue(t, r.Store, "durable", Input{Title: "A"}, Input{Title: "B"})
	r.Store.Control(ctx, batch.RequestIDs[0], "", "bump")
	r.ConfigureRoute(ctx, "books", "ebook", "kavita", "3", filepath.Join(dir, "books"))
	r.Store.db.Exec(`INSERT INTO grants VALUES('hash','owner','fixture','status',0,0)`)
	r.Store.db.Exec(`INSERT INTO grant_resources VALUES('hash',?)`, r.PublicURL)
	snapshot := filepath.Join(dir, "snapshot.db")
	if e := r.Store.Backup(ctx, snapshot); e != nil {
		t.Fatal(e)
	}
	if e := r.Store.Backup(ctx, snapshot); e == nil {
		t.Fatal("backup overwrote existing snapshot")
	}
	restored := runtimeTest(t, snapshot)
	if restored.InstanceID == "" || restored.InstanceID != r.InstanceID {
		t.Fatal("persistent instance identity changed")
	}
	if got := titles(t, restored.Store); !reflect.DeepEqual(got, []string{"A", "B"}) {
		t.Fatal(got)
	}
	replay := enqueue(t, restored.Store, "durable", Input{Title: "A"}, Input{Title: "B"})
	if !reflect.DeepEqual(batch, replay) {
		t.Fatal(batch, replay)
	}
	accounts, e := restored.Accounts(ctx)
	if e != nil || len(accounts) != 1 {
		t.Fatal(accounts, e)
	}
	_, password, e := restored.AccountCredentials(ctx, accounts[0].ID)
	if e != nil || password != "fixture-password" {
		t.Fatal("account ciphertext did not restore", e)
	}
	routes, e := restored.StoredRoutes(ctx)
	if e != nil || len(routes) != 1 || routes[0].Category != "books" {
		t.Fatal(routes, e)
	}
	var grant string
	if e = restored.Store.db.QueryRow(`SELECT owner FROM grants WHERE hash='hash'`).Scan(&grant); e != nil || grant != "owner" {
		t.Fatal(grant, e)
	}
	if _, e = NewRuntime(restored.Store, bytes.Repeat([]byte{10}, 32)); e == nil {
		t.Fatal("wrong encryption key accepted")
	}
}
func TestOfflineLegacyAccountImportDoesNotOverwrite(t *testing.T) {
	dir := t.TempDir()
	legacyPath := filepath.Join(dir, "approved-backup.db")
	legacy, e := sql.Open("sqlite3", legacyPath)
	if e != nil {
		t.Fatal(e)
	}
	legacy.Exec(`CREATE TABLE zlibuser(id INTEGER PRIMARY KEY,email TEXT,password TEXT)`)
	legacy.Exec(`INSERT INTO zlibuser VALUES(1,'fixture@example.invalid','legacy-password')`)
	legacy.Close()
	before, _ := os.ReadFile(legacyPath)
	r := runtimeTest(t, filepath.Join(dir, "jobs.db"))
	checks := 0
	r.ValidateAccount = func(context.Context, string, string) (*int, *int64, error) { checks++; n := 8; return &n, nil, nil }
	result, e := r.ImportLegacyAccounts(ctx, legacyPath)
	if e != nil || result.Imported != 1 {
		t.Fatal(result, e)
	}
	result, e = r.ImportLegacyAccounts(ctx, legacyPath)
	if e != nil || result.Existing != 1 || checks != 1 {
		t.Fatal(result, e, checks)
	}
	after, _ := os.ReadFile(legacyPath)
	if !bytes.Equal(before, after) {
		t.Fatal("source backup modified")
	}
}
