package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSeparateVolumesFromSamePackDoNotMerge(t *testing.T) {
	r := runtimeTest(t, filepath.Join(t.TempDir(), "jobs.db"))
	enqueue(t, r.Store, "volumes", Input{Title: "Example Manga", Kind: "manga", Volume: "1"}, Input{Title: "Example Manga", Kind: "manga", Volume: "2"})
	for i := 0; i < 2; i++ {
		l, e := r.Store.Claim(ctx, "manga", randomToken(), time.Now(), time.Minute)
		if e != nil {
			t.Fatal(e)
		}
		in, e := r.Store.input(ctx, l.RequestID)
		if e != nil {
			t.Fatal(e)
		}
		keep, e := r.Store.Canonicalize(ctx, l, in, Candidate{ID: "same-pack", Provider: "prowlarr", Title: "Example Manga v01-10", Format: "manga", Volume: "1-10"})
		if e != nil || !keep {
			t.Fatal("distinct requested volume merged", keep, e)
		}
	}
	list, e := r.Store.List(ctx, 10, 0, "queue")
	if e != nil || len(list) != 2 {
		t.Fatal(list, e)
	}
}

func TestLinkedOwnerCorrectionAndRetryUseActualJob(t *testing.T) {
	r := runtimeTest(t, filepath.Join(t.TempDir(), "jobs.db"))
	a := enqueue(t, r.Store, "a", Input{Title: "A", Formats: []string{"ebook"}})
	enqueue(t, r.Store, "b", Input{Title: "A subtitle", Formats: []string{"ebook"}})
	l, e := r.Store.Claim(ctx, "ebook", "owner", time.Now(), time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	if e = r.Store.RecordIntent(ctx, l, "fixture", "old", time.Now()); e != nil {
		t.Fatal(e)
	}
	in, _ := r.Store.input(ctx, l.RequestID)
	if keep, e := r.Store.Canonicalize(ctx, l, in, Candidate{ID: "old", Provider: "fixture", Title: "A", Format: "ebook"}); e != nil || !keep {
		t.Fatal(keep, e)
	}
	r.Sources = []Source{&effectSource{name: "fixture", r: r}}
	options, e := r.Candidates(ctx, a.RequestIDs[0], "ebook")
	if e != nil || len(options) != 1 {
		t.Fatal(options, e)
	}
	if e = r.Correct(ctx, a.RequestIDs[0], "ebook", options[0].ID); !errors.Is(e, ErrConflict) {
		t.Fatal("active linked transfer replaced", e)
	}
	r.Store.db.Exec(`UPDATE transfer_intents SET state='rejected' WHERE representation_id=?`, l.ID)
	r.Store.db.Exec(`UPDATE representations SET state='retryable_failure',lease_token=NULL,lease_until=NULL WHERE id=?`, l.ID)
	if e = r.Store.Control(ctx, a.RequestIDs[0], "ebook", "retry"); e != nil {
		t.Fatal(e)
	}
	enqueue(t, r.Store, "c", Input{Title: "C", Formats: []string{"ebook"}})
	r.Store.Control(ctx, a.RequestIDs[0], "", "bump")
	claimed, e := r.Store.Claim(ctx, "ebook", "new-owner", time.Now(), time.Minute)
	if e != nil || claimed.ID != l.ID {
		t.Fatal("retry/bump ignored linked owner", claimed, e)
	}
	r.Store.db.Exec(`UPDATE representations SET state='available',lease_token=NULL,lease_until=NULL WHERE id=?`, l.ID)
	r.Store.db.Exec(`UPDATE transfer_intents SET state='completed' WHERE representation_id=?`, l.ID)
	if e = r.Correct(ctx, a.RequestIDs[0], "ebook", options[0].ID); e != nil {
		t.Fatal(e)
	}
	var id int64
	r.Store.db.QueryRow(`SELECT job_id FROM candidate_choices`).Scan(&id)
	if id != l.ID {
		t.Fatal("correction targeted follower", id, l.ID)
	}
}

func TestCategoryRejectsActivePublication(t *testing.T) {
	r := runtimeTest(t, filepath.Join(t.TempDir(), "jobs.db"))
	l := claimTest(t, r, "A")
	r.ConfigureRoute(ctx, "new", "ebook", "kavita", "3", t.TempDir())
	r.Store.db.Exec(`UPDATE representations SET state='importing' WHERE id=?`, l.ID)
	if e := r.Categorize(ctx, []int64{l.RequestID}, "new"); !errors.Is(e, ErrConflict) {
		t.Fatal("active placement change acknowledged", e)
	}
	if category := r.category(ctx, l); category != "" {
		t.Fatal(category)
	}
	r.Store.db.Exec(`UPDATE representations SET lease_until=NULL,lease_token=NULL WHERE id=?`, l.ID)
	if e := r.Categorize(ctx, []int64{l.RequestID}, "new"); e != nil {
		t.Fatal(e)
	}
	if r.category(ctx, l) != "new" {
		t.Fatal("pending placement not updated")
	}
}

func TestVerifiedLocalImportDoesNotNeedSource(t *testing.T) {
	r := runtimeTest(t, filepath.Join(t.TempDir(), "jobs.db"))
	l := claimTest(t, r, "A")
	path := filepath.Join(t.TempDir(), "verified.epub")
	os.WriteFile(path, []byte("verified fixture"), 0600)
	r.Store.RecordIntent(ctx, l, "removed-provider", "remote", time.Now())
	data, _ := json.Marshal(Receipt{ID: "remote", State: "verified", Paths: []string{path}})
	r.Store.db.Exec(`INSERT INTO job_details(id,receipt) VALUES(?,?)`, l.ID, data)
	r.Store.db.Exec(`UPDATE representations SET state='importing',lease_until=NULL,lease_token=NULL,eligible_at=0 WHERE id=?`, l.ID)
	library := &fakeLibrary{}
	r.Library = library
	r.VerifyLocal = func(_ context.Context, _ string, paths []string) ([]string, error) {
		data, e := os.ReadFile(paths[0])
		if e != nil || string(data) != "verified fixture" {
			return nil, errors.New("invalid fixture")
		}
		return paths, nil
	}
	r.resume(ctx, "ebook")
	counts, _ := r.Store.Counts(ctx)
	if library.imports != 1 || counts["available"] != 1 {
		t.Fatal(library.imports, counts)
	}
}

func TestRefreshIntentSurvivesReopenAndCoalesces(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.db")
	r := runtimeTest(t, path)
	l := claimTest(t, r, "A")
	hooks := GetImportHooks(r.importContext(ctx, l))
	if e := hooks.Dirty("kavita:3"); e != nil {
		t.Fatal(e)
	}
	r.Store.Close()
	r = runtimeTest(t, path)
	hooks = GetImportHooks(r.importContext(ctx, l))
	claimed, e := hooks.ClaimRefresh("kavita:3")
	if e != nil || !claimed {
		t.Fatal(claimed, e)
	}
	if e = hooks.FinishRefresh("kavita:3", true); e != nil {
		t.Fatal(e)
	}
	hooks.Dirty("kavita:3")
	claimed, e = hooks.ClaimRefresh("kavita:3")
	if e != nil || claimed {
		t.Fatal("refresh not coalesced", claimed, e)
	}
}

func TestSelectedMetadataIsExposedWithoutPrivateURL(t *testing.T) {
	r := runtimeTest(t, filepath.Join(t.TempDir(), "jobs.db"))
	l := claimTest(t, r, "raw input")
	r.Store.saveCandidate(ctx, l, Candidate{ID: "private-token-id", Provider: "fixture", Title: "Chosen title", Author: "Chosen author", URL: "https://provider.invalid/?secret=private-token"})
	list, e := r.Store.List(ctx, 1, 0, "queue")
	if e != nil || list[0].Representations[0].Selected == nil {
		t.Fatal(list, e)
	}
	selected := list[0].Representations[0].Selected
	if selected.Title != "Chosen title" || selected.Author != "Chosen author" || selected.ID == "private-token-id" {
		t.Fatal(selected)
	}
}
func TestRepresentationBumpPersistsWithoutBumpingOtherLane(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.db")
	r := runtimeTest(t, path)
	a := enqueue(t, r.Store, "a", Input{Title: "A"})
	b := enqueue(t, r.Store, "b", Input{Title: "B"})
	if e := r.Store.Control(ctx, a.RequestIDs[0], "ebook", "bump"); e != nil {
		t.Fatal(e)
	}
	c := enqueue(t, r.Store, "c", Input{Title: "C"})
	r.Store.Close()
	r = runtimeTest(t, path)
	for _, lane := range []struct {
		format string
		want   []int64
	}{{"ebook", []int64{a.RequestIDs[0], c.RequestIDs[0], b.RequestIDs[0]}}, {"audio", []int64{c.RequestIDs[0], b.RequestIDs[0], a.RequestIDs[0]}}} {
		for _, want := range lane.want {
			lease, e := r.Store.Claim(ctx, lane.format, randomToken(), time.Now(), time.Minute)
			if e != nil || lease.RequestID != want {
				t.Fatal(lane.format, lease, want, e)
			}
		}
	}
}

type peerRecoverySource struct {
	acquired []string
	reused   bool
}

func (s *peerRecoverySource) Name() string { return "peer-fixture" }
func (s *peerRecoverySource) Search(_ context.Context, in Input, format string) ([]Candidate, error) {
	return []Candidate{{ID: "bad-peer", Provider: s.Name(), Title: in.Title, Format: format}, {ID: "good-peer", Provider: s.Name(), Title: in.Title, Format: format}}, nil
}
func (s *peerRecoverySource) Acquire(ctx context.Context, c Candidate, _ string) (Receipt, error) {
	s.acquired = append(s.acquired, c.ID)
	if c.ID == "bad-peer" {
		return Receipt{ID: c.ID, State: "provider_wait"}, nil
	}
	s.reused = len(RetainedFiles(ctx)) == 1
	return Receipt{ID: c.ID, State: "verified", Paths: []string{"fixture.mp3"}}, nil
}
func (s *peerRecoverySource) Reconcile(_ context.Context, r Receipt) (Receipt, error) {
	r.Completed = []RetainedFile{{Identity: "track-one", Path: "retained-track.mp3", Size: 1}}
	return r, RetryTransferFailure{errors.New("peer failed")}
}
func TestTerminalPeerRecoverySelectsAlternativeWithoutRequeueLoop(t *testing.T) {
	r := runtimeTest(t, filepath.Join(t.TempDir(), "jobs.db"))
	r.Library = &fakeLibrary{}
	source := &peerRecoverySource{}
	r.Sources = []Source{source}
	enqueue(t, r.Store, "audio", Input{Title: "A", Formats: []string{"audio"}})
	r.step(ctx, "audio")
	r.Store.db.Exec(`UPDATE representations SET eligible_at=0`)
	r.resume(ctx, "audio")
	r.Store.db.Exec(`UPDATE representations SET eligible_at=0`)
	r.step(ctx, "audio")
	if len(source.acquired) != 2 || source.acquired[1] != "good-peer" || !source.reused {
		t.Fatal(source)
	}
	counts, _ := r.Store.Counts(ctx)
	if counts["available"] != 1 {
		t.Fatal(counts)
	}
}

func TestFreshResolvedAliasResumesCanceledOwner(t *testing.T) {
	r := runtimeTest(t, filepath.Join(t.TempDir(), "jobs.db"))
	a := enqueue(t, r.Store, "a", Input{Title: "A", Formats: []string{"ebook"}})
	r.Store.Control(ctx, a.RequestIDs[0], "ebook", "cancel")
	enqueue(t, r.Store, "variant", Input{Title: "A subtitle", Formats: []string{"ebook"}})
	l, e := r.Store.Claim(ctx, "ebook", "variant-owner", time.Now(), time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	in, _ := r.Store.input(ctx, l.RequestID)
	keep, e := r.Store.Canonicalize(ctx, l, in, Candidate{ID: "same-work", Provider: "fixture", Title: "A", Format: "ebook"})
	if e != nil || keep {
		t.Fatal(keep, e)
	}
	claimed, e := r.Store.Claim(ctx, "ebook", "resumed-owner", time.Now(), time.Minute)
	if e != nil || claimed.RequestID != a.RequestIDs[0] {
		t.Fatal("fresh intent left canceled owner stranded", claimed, e)
	}
}

// An uncategorized book is classified once among its format's routes before
// placement; an answer outside the configured names keeps the default route.
func TestPlacementCategoryClassifiesOnce(t *testing.T) {
	r := runtimeTest(t, filepath.Join(t.TempDir(), "jobs.db"))
	l := claimTest(t, r, "Self-Help")
	r.ConfigureRoute(ctx, "fiction", "ebook", "kavita", "1", t.TempDir())
	r.ConfigureRoute(ctx, "nonfiction", "ebook", "kavita", "3", t.TempDir())
	calls := 0
	answer := "nonfiction"
	r.ClassifyCategory = func(_ context.Context, in Input, format string, options []string) string {
		calls++
		if format != "ebook" || len(options) != 2 || options[0] != "fiction" {
			t.Fatalf("options %v for %s", options, format)
		}
		return answer
	}
	if got := r.placementCategory(ctx, l, Input{Title: "Self-Help"}, "ebook"); got != "nonfiction" {
		t.Fatal(got)
	}
	answer = "fiction"
	if got := r.placementCategory(ctx, l, Input{Title: "Self-Help"}, "ebook"); got != "nonfiction" || calls != 1 {
		t.Fatal("stored category not reused", got, calls)
	}
	other := claimTest(t, r, "Other")
	answer = "poetry"
	if got := r.placementCategory(ctx, other, Input{Title: "Other"}, "ebook"); got != "" {
		t.Fatal("unconfigured category accepted", got)
	}
}

// An ebook from Z-Library can be a manga volume: manga routes are offered to
// ebook jobs, and a job classified as manga is placed with the manga format.
func TestEbookClassifiedAsManga(t *testing.T) {
	r := runtimeTest(t, filepath.Join(t.TempDir(), "jobs.db"))
	l := claimTest(t, r, "Iron Lantern, Vol. 1")
	r.ConfigureRoute(ctx, "fiction", "ebook", "kavita", "1", t.TempDir())
	r.ConfigureRoute(ctx, "nonfiction", "ebook", "kavita", "3", t.TempDir())
	r.ConfigureRoute(ctx, "manga", "manga", "kavita", "2", t.TempDir())
	r.ConfigureRoute(ctx, "audiobooks", "audio", "audiobookshelf", "a", t.TempDir())
	r.ClassifyCategory = func(_ context.Context, in Input, format string, options []string) string {
		if strings.Join(options, ",") != "fiction,nonfiction,manga" {
			t.Fatalf("options %v for %s", options, format)
		}
		return "manga"
	}
	category := r.placementCategory(ctx, l, Input{Title: "Iron Lantern, Vol. 1"}, "ebook")
	if category != "manga" {
		t.Fatal(category)
	}
	if got := r.placementFormat(ctx, category, "ebook"); got != "manga" {
		t.Fatal("manga ebook placed as", got)
	}
	if got := r.placementFormat(ctx, "nonfiction", "ebook"); got != "ebook" {
		t.Fatal(got)
	}
	if got := r.placementFormat(ctx, "audiobooks", "ebook"); got != "ebook" {
		t.Fatal("cross-format route outside manga accepted", got)
	}
}

// A manga requested as a book is matched and named in the manga library by
// series and volume.
func TestPlacementInputSplitsTitleVolume(t *testing.T) {
	for title, want := range map[string][2]string{
		"Iron Lantern, Vol. 1":     {"Iron Lantern", "1"},
		"Supper in Stormhold V01": {"Supper in Stormhold", "01"},
		"Sea Glass Volume 100":     {"Sea Glass", "100"},
		"Elowen (Vol. 3)":         {"Elowen", "3"},
		"Engine 451":           {"Engine 451", ""},
		"Vol. 2":                   {"Vol. 2", ""},
	} {
		got := placementInput(Input{Title: title}, "manga")
		if got.Title != want[0] || got.Volume != want[1] {
			t.Errorf("%q -> %q %q", title, got.Title, got.Volume)
		}
	}
	if got := placementInput(Input{Title: "Iron Lantern, Vol. 1"}, "ebook"); got.Volume != "" {
		t.Fatal("ebook placement split", got)
	}
	if got := placementInput(Input{Title: "Iron Lantern, Vol. 1", Volume: "2"}, "manga"); got.Title != "Iron Lantern, Vol. 1" {
		t.Fatal("explicit volume overridden", got)
	}
}

// missAt claims and ends one ebook job as if the clock read now.
func missAt(t *testing.T, r *Runtime, now time.Time, state string) int64 {
	t.Helper()
	r.Store.db.Exec(`UPDATE representations SET state='queued' WHERE state='missing' AND eligible_at<=?`, now.Unix())
	l, e := r.Store.Claim(ctx, "ebook", randomToken(), now, time.Minute)
	if e != nil {
		t.Fatal("not claimable", e)
	}
	tx, _ := r.Store.db.Begin()
	message := ""
	next, e := stallBackoff(ctx, tx, l.ID, &state, &message, now)
	if e != nil {
		t.Fatal(e)
	}
	tx.Exec(`UPDATE representations SET state=?,eligible_at=?,lease_token=NULL,lease_until=NULL WHERE id=?`, state, next.Unix(), l.ID)
	tx.Commit()
	return next.Unix() - now.Unix()
}

func TestMissingRetriesThenLeavesQueueAfterSevenDays(t *testing.T) {
	r := runtimeTest(t, filepath.Join(t.TempDir(), "jobs.db"))
	b := enqueue(t, r.Store, "gone", Input{Title: "Unfindable", Formats: []string{"ebook"}})
	now := time.Now()
	waits := []int64{}
	for i := 0; i < 9; i++ {
		var state string
		r.Store.db.QueryRow(`SELECT state FROM representations`).Scan(&state)
		if state == "not_found" {
			break
		}
		wait := missAt(t, r, now, "missing")
		waits = append(waits, wait)
		now = now.Add(time.Duration(wait) * time.Second)
	}
	hours := []int64{1, 2, 4, 8, 16, 32, 64, 41}
	for i, h := range hours {
		if i >= len(waits) || waits[i] != h*3600 {
			t.Fatal("retry schedule", waits)
		}
	}
	var state string
	r.Store.db.QueryRow(`SELECT state FROM representations`).Scan(&state)
	if state != "not_found" || len(waits) != 9 {
		t.Fatal("not removed after the seven-day retry", state, waits)
	}
	if _, e := r.Store.Claim(ctx, "ebook", randomToken(), now.Add(30*24*time.Hour), time.Minute); e == nil {
		t.Fatal("not_found job claimed again")
	}
	list, _ := r.Store.List(ctx, 10, 0, "queue")
	if list[0].Representations[0].RetryAt != 0 {
		t.Fatal("removed job advertises a retry")
	}
	if e := r.Store.Control(ctx, b.RequestIDs[0], "ebook", "retry"); e != nil {
		t.Fatal(e)
	}
	if wait := missAt(t, r, now, "missing"); wait != 3600 {
		t.Fatal("manual retry did not start a fresh window", wait)
	}
}

func TestMissingShowsNextRetry(t *testing.T) {
	r := runtimeTest(t, filepath.Join(t.TempDir(), "jobs.db"))
	enqueue(t, r.Store, "gone", Input{Title: "Unfindable", Formats: []string{"ebook"}})
	missAt(t, r, time.Now(), "missing")
	list, _ := r.Store.List(ctx, 10, 0, "queue")
	if at := list[0].Representations[0].RetryAt; at < time.Now().Add(59*time.Minute).Unix() {
		t.Fatal("retry time not reported", at)
	}
}

// Unfindable requests at the front of the queue must not keep the lane from
// the work behind them, however many there are.
func TestUnfindableRequestsCannotStarveTheLane(t *testing.T) {
	r := runtimeTest(t, filepath.Join(t.TempDir(), "jobs.db"))
	for i := 0; i < 50; i++ {
		enqueue(t, r.Store, fmt.Sprint("gone", i), Input{Title: fmt.Sprint("Unfindable ", i), Formats: []string{"ebook"}})
	}
	enqueue(t, r.Store, "real", Input{Title: "Findable", Formats: []string{"ebook"}})
	r.Store.db.Exec(`UPDATE requests SET bump=1 WHERE title LIKE 'Unfindable%'`)
	now := time.Now()
	for step := 0; step < 51; step++ {
		r.Store.db.Exec(`UPDATE representations SET state='queued' WHERE state IN ('missing','waiting_provider') AND eligible_at<=?`, now.Unix())
		l, e := r.Store.Claim(ctx, "ebook", randomToken(), now, time.Minute)
		if e != nil {
			t.Fatal("lane idle", e)
		}
		in, _ := r.Store.input(ctx, l.RequestID)
		if in.Title == "Findable" {
			return
		}
		tx, _ := r.Store.db.Begin()
		state, message := "missing", ""
		next, _ := stallBackoff(ctx, tx, l.ID, &state, &message, now)
		tx.Exec(`UPDATE representations SET state=?,eligible_at=?,lease_token=NULL WHERE id=?`, state, next.Unix(), l.ID)
		tx.Commit()
		now = now.Add(2 * time.Minute)
	}
	t.Fatal("findable request never reached")
}

func TestBlockedProviderWaitIsBounded(t *testing.T) {
	r := runtimeTest(t, filepath.Join(t.TempDir(), "jobs.db"))
	enqueue(t, r.Store, "blocked", Input{Title: "Blocked", Formats: []string{"ebook"}})
	now := time.Now()
	waits := []int64{}
	for i := 0; i < 9; i++ {
		r.Store.db.Exec(`UPDATE representations SET state='queued',eligible_at=0`)
		waits = append(waits, missAt(t, r, now, "waiting_provider"))
	}
	if waits[0] != 60 || waits[1] != 120 || waits[8] != 3600 {
		t.Fatal("provider wait schedule", waits)
	}
}
