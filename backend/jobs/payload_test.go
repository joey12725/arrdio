package jobs

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
)

type pendingPayloadSource struct{ reconciles int }

func (s *pendingPayloadSource) Name() string { return "torrents" }
func (s *pendingPayloadSource) Search(context.Context, Input, string) ([]Candidate, error) {
	return []Candidate{{ID: "release", Provider: "torrents", Title: "A", Format: "ebook"}}, nil
}
func (s *pendingPayloadSource) Prepare(context.Context, Candidate, string) (Receipt, error) {
	return Receipt{ID: "hash", State: "transferring"}, nil
}
func (s *pendingPayloadSource) Acquire(context.Context, Candidate, string) (Receipt, error) {
	return Receipt{ID: "hash", State: "transferring"}, nil
}
func (s *pendingPayloadSource) Reconcile(_ context.Context, r Receipt) (Receipt, error) {
	s.reconciles++
	if s.reconciles == 1 {
		return r, fmt.Errorf("%w: completed payload not mounted", ErrPayloadPending)
	}
	r.State, r.Paths = "verified", []string{"fixture.epub"}
	return r, nil
}

// A finished download whose files are not yet readable concerns that job only.
// Live, backing off the whole source sent unrelated requests to worse sources.
func TestPendingPayloadDoesNotBackOffProvider(t *testing.T) {
	r := runtimeTest(t, filepath.Join(t.TempDir(), "jobs.db"))
	library := &fakeLibrary{}
	r.Library = library
	source := &pendingPayloadSource{}
	r.Sources = []Source{source}
	enqueue(t, r.Store, "a", Input{Title: "A", Formats: []string{"ebook"}})
	r.step(ctx, "ebook")
	r.Store.db.Exec(`UPDATE representations SET lease_until=0,eligible_at=0`)
	r.resume(ctx, "ebook")
	if r.sourceDeferred(ctx, "torrents") {
		t.Fatal("pending payload backed off the provider")
	}
	var state, message string
	r.Store.db.QueryRow(`SELECT r.state,COALESCE(d.error,'') FROM representations r LEFT JOIN job_details d ON d.id=r.id`).Scan(&state, &message)
	if state != "retryable_failure" || message != "Download complete; waiting for its files to become readable" {
		t.Fatal(state, message)
	}
	r.Store.db.Exec(`UPDATE representations SET lease_until=0,eligible_at=0`)
	r.resume(ctx, "ebook")
	if library.imports != 1 {
		t.Fatal("readable payload not imported", source.reconciles, library.imports)
	}
}

func TestNewPhaseClearsPreviousError(t *testing.T) {
	r := runtimeTest(t, filepath.Join(t.TempDir(), "jobs.db"))
	l := claimTest(t, r, "A")
	r.Store.db.Exec(`INSERT INTO job_details(id,error,updated) VALUES(?,?,0) ON CONFLICT(id) DO UPDATE SET error=excluded.error`, l.ID, "Transfer outcome unresolved; no repeated source charge")
	if !r.Store.phase(ctx, l, "importing") {
		t.Fatal("phase not entered")
	}
	var message string
	r.Store.db.QueryRow(`SELECT error FROM job_details WHERE id=?`, l.ID).Scan(&message)
	if message != "" {
		t.Fatal("stale error shown during new phase:", message)
	}
}
