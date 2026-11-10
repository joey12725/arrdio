package jobs

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"
)

var ctx = context.Background()

func openTest(t *testing.T, path string) *Store {
	t.Helper()
	s, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
func enqueue(t *testing.T, s *Store, key string, in ...Input) Batch {
	t.Helper()
	b, e := s.Enqueue(ctx, "household-test", key, in)
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func titles(t *testing.T, s *Store) []string {
	t.Helper()
	rs, e := s.List(ctx, 100, 0, "queue")
	if e != nil {
		t.Fatal(e)
	}
	out := []string{}
	for _, r := range rs {
		out = append(out, r.Title)
	}
	return out
}

func TestLIFOReplayBumpRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.db")
	s := openTest(t, path)
	b := enqueue(t, s, "abc", Input{Title: "A"}, Input{Title: "B"}, Input{Title: "C"})
	if got := titles(t, s); !reflect.DeepEqual(got, []string{"C", "B", "A"}) {
		t.Fatal(got)
	}
	if e := s.Control(ctx, b.RequestIDs[0], "", "bump"); e != nil {
		t.Fatal(e)
	}
	enqueue(t, s, "d", Input{Title: "D"})
	replay := enqueue(t, s, "abc", Input{Title: "A"}, Input{Title: "B"}, Input{Title: "C"})
	if !reflect.DeepEqual(b, replay) {
		t.Fatal(replay)
	}
	s.Close()
	s = openTest(t, path)
	if got := titles(t, s); !reflect.DeepEqual(got, []string{"A", "D", "C", "B"}) {
		t.Fatal(got)
	}
	for _, rid := range []int64{b.RequestIDs[0], 4, b.RequestIDs[2], b.RequestIDs[1]} {
		l, e := s.Claim(ctx, "ebook", fmt.Sprint(rid), time.Now(), time.Minute)
		if e != nil || l.RequestID != rid {
			t.Fatalf("%+v %v", l, e)
		}
	}
	if _, e := s.Claim(ctx, "ebook", "extra", time.Now(), time.Minute); !errors.Is(e, sql.ErrNoRows) {
		t.Fatal(e)
	}
}

func TestBulkPersistenceAndFormats(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.db")
	s := openTest(t, path)
	inputs := make([]Input, 10000)
	for i := range inputs {
		inputs[i] = Input{Title: fmt.Sprintf("Book %05d", i)}
	}
	b := enqueue(t, s, "bulk", inputs...)
	if len(b.RequestIDs) != 10000 {
		t.Fatal(len(b.RequestIDs))
	}
	enqueue(t, s, "duplicate", Input{Title: " Book   00000 ", Formats: []string{"ebook"}})
	s.Close()
	s = openTest(t, path)
	counts, e := s.Counts(ctx)
	if e != nil || counts["queued"] != 20000 {
		t.Fatalf("%v %v", counts, e)
	}
	rs, e := s.List(ctx, 50, 9950, "title")
	if e != nil || len(rs) != 50 || rs[0].Title != "Book 09950" {
		t.Fatalf("page: %d %v", len(rs), e)
	}
	manga := enqueue(t, s, "manga", Input{Title: "Manga", Kind: "manga", Volume: "1"})
	rs, e = s.List(ctx, 1, 0, "queue")
	if e != nil || rs[0].ID != manga.RequestIDs[0] || len(rs[0].Representations) != 1 || rs[0].Representations[0].Format != "manga" {
		t.Fatalf("%+v %v", rs, e)
	}
}

func TestDedupCancelAndValidation(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "jobs.db"))
	b := enqueue(t, s, "one", Input{Title: "A", Formats: []string{"ebook"}})
	b2 := enqueue(t, s, "two", Input{Title: "a", Formats: []string{"audio"}})
	if b.RequestIDs[0] != b2.RequestIDs[0] {
		t.Fatal("duplicate parent")
	}
	if _, e := s.Enqueue(ctx, "household-test", "one", []Input{{Title: "different"}}); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	if e := s.Control(ctx, b.RequestIDs[0], "ebook", "cancel"); e != nil {
		t.Fatal(e)
	}
	counts, e := s.Counts(ctx)
	if e != nil || counts["canceled"] != 1 || counts["queued"] != 1 {
		t.Fatalf("%v %v", counts, e)
	}
	partial, e := s.Enqueue(ctx, "test", "mixed", []Input{{Title: "valid"}, {Title: ""}})
	if e != nil || len(partial.RequestIDs) != 1 || len(partial.Errors) != 1 || partial.Errors[0].Index != 1 {
		t.Fatal(partial, e)
	}
	if got := titles(t, s); len(got) != 2 {
		t.Fatal("valid item lost from mixed batch", got)
	}
}

func TestLeaseFencingAndAmbiguousIntent(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "jobs.db"))
	enqueue(t, s, "one", Input{Title: "A"})
	now := time.Now()
	l, e := s.Claim(ctx, "ebook", "old", now, time.Second)
	if e != nil {
		t.Fatal(e)
	}
	next := now.Add(2 * time.Second)
	fresh, e := s.Claim(ctx, "ebook", "new", next, time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.RecordIntent(ctx, l, "zlibrary", "candidate", next); e == nil {
		t.Fatal("stale lease accepted")
	}
	if e = s.RecordIntent(ctx, fresh, "zlibrary", "candidate", next); e != nil {
		t.Fatal(e)
	}
	if e = s.RecordIntent(ctx, fresh, "zlibrary", "candidate", next); e == nil {
		t.Fatal("duplicate intent")
	}
	if _, e = s.Claim(ctx, "ebook", "after-crash", now.Add(time.Hour), time.Minute); !errors.Is(e, sql.ErrNoRows) {
		t.Fatal("ambiguous transfer redispatched", e)
	}
	if _, e = s.Claim(ctx, "audio", "other-lane", next, time.Minute); e != nil {
		t.Fatal(e)
	}
}

func TestConcurrentStoresClaimOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.db")
	a := openTest(t, path)
	b := openTest(t, path)
	enqueue(t, a, "one", Input{Title: "A", Formats: []string{"ebook"}})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for i, s := range []*Store{a, b} {
		wg.Add(1)
		go func(i int, s *Store) {
			defer wg.Done()
			_, e := s.Claim(ctx, "ebook", fmt.Sprint(i), time.Now(), time.Minute)
			results <- e
		}(i, s)
	}
	wg.Wait()
	close(results)
	wins := 0
	for e := range results {
		if e == nil {
			wins++
		} else if !errors.Is(e, sql.ErrNoRows) {
			t.Fatal(e)
		}
	}
	if wins != 1 {
		t.Fatal(wins)
	}
}
