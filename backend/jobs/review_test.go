package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func claimTest(t *testing.T, r *Runtime, title string) Lease {
	t.Helper()
	enqueue(t, r.Store, title, Input{Title: title, Formats: []string{"ebook"}})
	l, e := r.Store.Claim(ctx, "ebook", randomToken(), time.Now(), time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	return l
}
func seedAccount(t *testing.T, r *Runtime) {
	t.Helper()
	n := 8
	r.ValidateAccount = func(context.Context, string, string) (*int, *int64, error) { return &n, nil, nil }
	if e := r.AddAccount(ctx, "test@example.invalid", "fixture-password"); e != nil {
		t.Fatal(e)
	}
}
func TestCanceledAndCrashedReservationRecovery(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.db")
	r := runtimeTest(t, path)
	seedAccount(t, r)
	l := claimTest(t, r, "A")
	if _, e := r.Reserve(ctx, l.ID); e != nil {
		t.Fatal(e)
	}
	if e := r.Store.Control(ctx, l.RequestID, "ebook", "cancel"); e != nil {
		t.Fatal(e)
	}
	r.Store.Close()
	r = runtimeTest(t, path)
	seedAccount(t, r)
	other := claimTest(t, r, "B")
	if _, e := r.Reserve(ctx, other.ID); e != nil {
		t.Fatal("canceled reservation stranded account", e)
	}
	if e := r.Store.RecordIntent(ctx, other, "zlibrary", "known-book", time.Now()); e != nil {
		t.Fatal(e)
	}
	r.Store.db.Exec(`UPDATE representations SET lease_until=0 WHERE id=?`, other.ID)
	r.recoverReservations(ctx)
	var state string
	r.Store.db.QueryRow(`SELECT state FROM reservations WHERE job_id=?`, other.ID).Scan(&state)
	if state != "uncertain" {
		t.Fatal(state)
	}
	third := claimTest(t, r, "C")
	if _, e := r.Reserve(ctx, third.ID); e == nil {
		t.Fatal("account reused before fresh profile")
	}
	r.RefreshAccounts(ctx)
	if _, e := r.Reserve(ctx, third.ID); e != nil {
		t.Fatal("account not released after reconciliation", e)
	}
}
func TestQuotaBoundaryFiveZeroUnknownAndStale(t *testing.T) {
	r := runtimeTest(t, filepath.Join(t.TempDir(), "jobs.db"))
	seedAccount(t, r)
	r.AddAccount(ctx, "other@example.invalid", "fixture-password")
	reset := time.Now().Add(time.Hour).Unix()
	for _, test := range []struct {
		name       string
		a, b       any
		stale      bool
		knownReset bool
		want       any
		timer      bool
	}{{"five", 1, 4, false, true, 5, false}, {"four", 1, 3, false, true, 4, true}, {"zero", 0, 0, false, true, 0, true}, {"individual low", 1, 8, false, true, 9, false}, {"unknown allowance", nil, 4, false, true, nil, false}, {"unknown reset", 1, 3, false, false, 4, false}, {"stale", 1, 3, true, true, nil, false}} {
		t.Run(test.name, func(t *testing.T) {
			var resetValue any
			if test.knownReset {
				resetValue = reset
			}
			checked := time.Now().Unix()
			if test.stale {
				checked -= 601
			}
			r.Store.db.Exec(`UPDATE accounts SET remaining=?,reset=?,checked=?,healthy=1 WHERE id=1`, test.a, resetValue, checked)
			r.Store.db.Exec(`UPDATE accounts SET remaining=?,reset=?,checked=?,healthy=1 WHERE id=2`, test.b, resetValue, checked)
			q, e := r.Quota(ctx)
			if e != nil || q["remaining"] != test.want {
				t.Fatal(q, e)
			}
			shown := false
			if value, ok := q["reset"].(*int64); ok {
				shown = value != nil
			}
			if shown != test.timer {
				t.Fatal(q)
			}
		})
	}
}

type effectSource struct {
	name          string
	r             *Runtime
	safe          bool
	cancel        bool
	preflightSeen bool
	acquires      int
}

func (s *effectSource) Name() string { return s.name }
func (s *effectSource) Search(context.Context, Input, string) ([]Candidate, error) {
	return []Candidate{{ID: "candidate-" + s.name, Provider: s.name, Title: "A", Format: "ebook"}}, nil
}
func (s *effectSource) Prepare(context.Context, Candidate, string) (Receipt, error) {
	return Receipt{ID: "remote-" + s.name, State: "transferring"}, nil
}
func (s *effectSource) Acquire(ctx context.Context, c Candidate, d string) (Receipt, error) {
	s.acquires++
	var data []byte
	s.r.Store.db.QueryRow(`SELECT receipt FROM job_details LIMIT 1`).Scan(&data)
	var receipt Receipt
	json.Unmarshal(data, &receipt)
	s.preflightSeen = receipt.ID == "remote-"+s.name
	if s.safe {
		return Receipt{}, SafeFailure{errors.New("authentication rejected before submit")}
	}
	if s.cancel {
		var id int64
		s.r.Store.db.QueryRow(`SELECT id FROM requests LIMIT 1`).Scan(&id)
		s.r.Store.Control(ctx, id, "ebook", "cancel")
	}
	return Receipt{ID: receipt.ID, State: "verified", Paths: []string{"fixture.epub"}}, nil
}
func (s *effectSource) Reconcile(_ context.Context, r Receipt) (Receipt, error) {
	if r.ID != "remote-"+s.name {
		return r, errors.New("missing remote identity")
	}
	r.State = "verified"
	r.Paths = []string{"fixture.epub"}
	return r, nil
}
func TestPreparedReceiptAndSafeProviderFallback(t *testing.T) {
	r := runtimeTest(t, filepath.Join(t.TempDir(), "jobs.db"))
	r.Library = &fakeLibrary{}
	a := &effectSource{name: "first", r: r, safe: true}
	b := &effectSource{name: "second", r: r}
	r.Sources = []Source{a, b}
	enqueue(t, r.Store, "a", Input{Title: "A", Formats: []string{"ebook"}})
	r.step(ctx, "ebook")
	if !a.preflightSeen {
		t.Fatal("submission before durable receipt")
	}
	r.Store.db.Exec(`UPDATE representations SET eligible_at=0`)
	r.step(ctx, "ebook")
	if a.acquires != 1 || b.acquires != 1 {
		t.Fatal("definite failure did not fall back", a.acquires, b.acquires)
	}
}
func TestCancelDuringAcquireCannotImport(t *testing.T) {
	r := runtimeTest(t, filepath.Join(t.TempDir(), "jobs.db"))
	library := &fakeLibrary{}
	r.Library = library
	source := &effectSource{name: "cancel", r: r, cancel: true}
	r.Sources = []Source{source}
	enqueue(t, r.Store, "a", Input{Title: "A", Formats: []string{"ebook"}})
	r.step(ctx, "ebook")
	if library.imports != 0 {
		t.Fatal("canceled work imported")
	}
	counts, _ := r.Store.Counts(ctx)
	if counts["canceled"] != 1 {
		t.Fatal(counts)
	}
}
func TestCrashBeforeReceiptWriteReconstructsRemoteIdentity(t *testing.T) {
	r := runtimeTest(t, filepath.Join(t.TempDir(), "jobs.db"))
	library := &fakeLibrary{}
	r.Library = library
	source := &effectSource{name: "remote", r: r}
	r.Sources = []Source{source}
	l := claimTest(t, r, "A")
	candidate := Candidate{ID: "known-source", Provider: "remote", Title: "A", Format: "ebook"}
	r.Store.saveCandidate(ctx, l, candidate)
	if e := r.Store.RecordIntent(ctx, l, "remote", candidate.ID, time.Now()); e != nil {
		t.Fatal(e)
	}
	r.Store.db.Exec(`UPDATE representations SET lease_until=0,eligible_at=0 WHERE id=?`, l.ID)
	r.resume(ctx, "ebook")
	if library.imports != 1 || source.acquires != 0 {
		t.Fatal("recovery resubmitted or lost identity", library.imports, source.acquires)
	}
}
func TestCanonicalCatalogAndResolvedAliases(t *testing.T) {
	r := runtimeTest(t, filepath.Join(t.TempDir(), "jobs.db"))
	a := enqueue(t, r.Store, "text", Input{Title: "A", Author: "Author"})
	catalog := enqueue(t, r.Store, "catalog", Input{Title: "A", Author: "Author", CatalogID: "/works/1"})
	if a.RequestIDs[0] != catalog.RequestIDs[0] {
		t.Fatal("catalog duplicates text")
	}
	b := enqueue(t, r.Store, "photo", Input{Title: "A subtitle", Author: "Author"})
	l, e := r.Store.Claim(ctx, "ebook", "owner", time.Now(), time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	keep, e := r.Store.Canonicalize(ctx, l, Input{Title: "A subtitle", Author: "Author", Kind: "book"}, Candidate{ID: "source-id", Provider: "fixture", Title: "A", Author: "Author"})
	if e != nil || keep {
		t.Fatal(keep, e)
	}
	list, e := r.Store.List(ctx, 20, 0, "queue")
	if e != nil || len(list) != 1 {
		t.Fatal(len(list), e)
	}
	if e = r.Store.Control(ctx, b.RequestIDs[0], "ebook", "cancel"); e != nil {
		t.Fatal("old merged ID unusable", e)
	}
}
func TestCategoryIsFormatAwareAndQueuesPlacement(t *testing.T) {
	r := runtimeTest(t, filepath.Join(t.TempDir(), "jobs.db"))
	b := enqueue(t, r.Store, "a", Input{Title: "A"})
	r.ConfigureRoute(ctx, "audio-main", "audio", "abs", "1", t.TempDir())
	r.ConfigureRoute(ctx, "ebook-main", "ebook", "kavita", "2", t.TempDir())
	if e := r.Categorize(ctx, b.RequestIDs, "audio-main"); e != nil {
		t.Fatal(e)
	}
	var format string
	r.Store.db.QueryRow(`SELECT format FROM representations WHERE id=(SELECT job_id FROM representation_categories)`).Scan(&format)
	if format != "audio" {
		t.Fatal(format)
	}
	var id int64
	r.Store.db.QueryRow(`SELECT id FROM representations WHERE format='audio'`).Scan(&id)
	r.Store.db.Exec(`UPDATE representations SET state='available' WHERE id=?`, id)
	if e := r.Categorize(ctx, b.RequestIDs, "audio-main"); e == nil {
		t.Fatal("library-only item silently relabeled")
	}
	raw, _ := json.Marshal(Receipt{ID: "remote", State: "verified", Paths: []string{"book.mp3"}})
	r.Store.db.Exec(`INSERT INTO job_details(id,receipt) VALUES(?,?)`, id, raw)
	if e := r.Categorize(ctx, b.RequestIDs, "audio-main"); e != nil {
		t.Fatal(e)
	}
	var state string
	r.Store.db.QueryRow(`SELECT state FROM representations WHERE id=?`, id).Scan(&state)
	if state != "importing" {
		t.Fatal(state)
	}
}
func TestPhotoTransientFailureRetainsDataAndStableResult(t *testing.T) {
	r := runtimeTest(t, filepath.Join(t.TempDir(), "jobs.db"))
	calls := 0
	r.ExtractPhoto = func(context.Context, []byte, string) ([]Input, error) {
		calls++
		if calls == 1 {
			return nil, errors.New("temporary transport error")
		}
		return []Input{{Title: "Readable book"}}, nil
	}
	r.Store.db.Exec(`INSERT INTO photo_units(hash,owner,data,mime,created) VALUES('photo','test',?,'image/png',0)`, []byte("fixture"))
	r.processPhoto(ctx)
	var data []byte
	var state string
	r.Store.db.QueryRow(`SELECT state,data FROM photo_units WHERE hash='photo'`).Scan(&state, &data)
	if state != "queued" || len(data) == 0 {
		t.Fatal(state, len(data))
	}
	r.Store.db.Exec(`UPDATE photo_retries SET next_at=0`)
	r.processPhoto(ctx)
	r.processPhoto(ctx)
	counts, _ := r.Store.Counts(ctx)
	if calls != 2 || counts["queued"] != 2 {
		t.Fatal(calls, counts)
	}
}

type audioFixtureSource struct{ effectSource }

func (s *audioFixtureSource) Search(context.Context, Input, string) ([]Candidate, error) {
	return []Candidate{{ID: "audio", Provider: s.name, Title: "A", Format: "audio"}}, nil
}
func (s *audioFixtureSource) Acquire(context.Context, Candidate, string) (Receipt, error) {
	s.acquires++
	return Receipt{ID: "remote-" + s.name, State: "verified", Paths: []string{"preserved-original.mp3"}}, nil
}
func (s *audioFixtureSource) Reconcile(_ context.Context, r Receipt) (Receipt, error) {
	r.State = "verified"
	r.Paths = []string{"preserved-original.mp3"}
	return r, nil
}
func TestConversionInterruptionRecoversWithoutReacquisition(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.db")
	r := runtimeTest(t, path)
	r.Library = &fakeLibrary{}
	source := &audioFixtureSource{effectSource{name: "audio", r: r}}
	r.Sources = []Source{source}
	enqueue(t, r.Store, "audio", Input{Title: "A", Formats: []string{"audio"}})
	runCtx, cancel := context.WithCancel(ctx)
	r.ConvertAudio = func(context.Context, []string, string, string, string) (string, error) {
		cancel()
		return "", context.Canceled
	}
	r.step(runCtx, "audio")
	var state string
	r.Store.db.QueryRow(`SELECT state FROM conversions`).Scan(&state)
	if state != "running" || source.acquires != 1 {
		t.Fatal(state, source.acquires)
	}
	r.Store.Close()
	r = runtimeTest(t, path)
	library := &fakeLibrary{}
	r.Library = library
	source = &audioFixtureSource{effectSource{name: "audio", r: r}}
	r.Sources = []Source{source}
	r.ConvertAudio = func(_ context.Context, paths []string, destination, _, _ string) (string, error) {
		if len(paths) != 1 || paths[0] != "preserved-original.mp3" {
			t.Fatal(paths)
		}
		return destination, os.WriteFile(destination, []byte("conversion fixture"), 0600)
	}
	r.Store.db.Exec(`UPDATE representations SET lease_until=0,eligible_at=0`)
	r.resume(ctx, "audio")
	counts, _ := r.Store.Counts(ctx)
	if source.acquires != 0 || library.imports != 1 || counts["available"] != 1 {
		t.Fatal(source.acquires, library.imports, counts)
	}
}
