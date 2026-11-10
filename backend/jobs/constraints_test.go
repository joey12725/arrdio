package jobs

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestHardConstraintsPersistAndDoNotMergeWithPermissiveRequest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.db")
	r := runtimeTest(t, path)
	plain := enqueue(t, r.Store, "plain", Input{Title: "A", CatalogID: "work-1"})
	hard := enqueue(t, r.Store, "hard", Input{Title: "A", CatalogID: "work-1", Constraints: Constraints{Language: " English ", Narrator: "Reader", Container: "m4b"}})
	if plain.RequestIDs[0] == hard.RequestIDs[0] {
		t.Fatal("constraint intent lost in dedup")
	}
	r.Store.Close()
	r = runtimeTest(t, path)
	in, e := r.Store.input(ctx, hard.RequestIDs[0])
	if e != nil || in.Constraints.Language != "english" || in.Constraints.Container != "m4b" {
		t.Fatal(in, e)
	}
	candidate := Candidate{Language: "English", Narrator: "Reader", Extension: "m4b"}
	if !in.Allows(candidate, "audio") {
		t.Fatal("verified exact constraints rejected")
	}
	candidate.Narrator = ""
	if in.Allows(candidate, "audio") {
		t.Fatal("unknown narrator accepted as exact")
	}
}

func TestMergePreservesBothOutstandingSiblingTransfers(t *testing.T) {
	r := runtimeTest(t, filepath.Join(t.TempDir(), "jobs.db"))
	a := enqueue(t, r.Store, "first", Input{Title: "A"})
	b := enqueue(t, r.Store, "second", Input{Title: "A subtitle"})
	for _, rid := range []int64{a.RequestIDs[0], b.RequestIDs[0]} {
		var job int64
		r.Store.db.QueryRow(`SELECT id FROM representations WHERE request_id=? AND format='audio'`, rid).Scan(&job)
		r.Store.db.Exec(`UPDATE representations SET state='transferring' WHERE id=?`, job)
		r.Store.db.Exec(`INSERT INTO transfer_intents(representation_id,provider,candidate_id) VALUES(?,'fixture',?)`, job, rid)
	}
	lease, e := r.Store.Claim(ctx, "ebook", "merge-owner", time.Now(), time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	in, _ := r.Store.input(ctx, lease.RequestID)
	if _, e = r.Store.Canonicalize(ctx, lease, in, Candidate{Provider: "fixture", ID: "same-work", Title: "A"}); !errors.Is(e, ErrMergePending) {
		t.Fatal(e)
	}
	var active, linked int
	r.Store.db.QueryRow(`SELECT count(*) FROM representations WHERE format='audio' AND state='transferring'`).Scan(&active)
	r.Store.db.QueryRow(`SELECT count(*) FROM linked_jobs`).Scan(&linked)
	if active != 2 || linked != 0 {
		t.Fatal("outstanding receipt orphaned", active, linked)
	}
}
func TestFailedConversionCannotSatisfyRequiredM4B(t *testing.T) {
	in := Input{Constraints: Constraints{Container: "m4b"}}
	if requiredOutput(in, Receipt{Paths: []string{"original.mp3"}}) {
		t.Fatal("playable fallback overrode hard container")
	}
	if !requiredOutput(in, Receipt{Paths: []string{"normalized.m4b"}}) {
		t.Fatal("required output rejected")
	}
}
func TestLanguageRegionHandling(t *testing.T) {
	if !LanguageMatches("English", "en-US") || LanguageMatches("en-GB", "en-US") || LanguageMatches("English", "fr") {
		t.Fatal("language region handling failed")
	}
	// Bibliographic ISO 639-2 codes, as used in MyAnonamouse release tags.
	if !LanguageMatches("French", "fre") || !LanguageMatches("de", "GER") || !LanguageMatches("Dutch", "dut") || LanguageMatches("English", "dan") {
		t.Fatal("ISO 639-2 language codes")
	}
}
func TestMergeRetainsSoleUnresolvedOwnerAgainstRejectedSibling(t *testing.T) {
	r := runtimeTest(t, filepath.Join(t.TempDir(), "jobs.db"))
	a := enqueue(t, r.Store, "a", Input{Title: "A"})
	b := enqueue(t, r.Store, "b", Input{Title: "A subtitle"})
	var first, second int64
	r.Store.db.QueryRow(`SELECT id FROM representations WHERE request_id=? AND format='audio'`, a.RequestIDs[0]).Scan(&first)
	r.Store.db.QueryRow(`SELECT id FROM representations WHERE request_id=? AND format='audio'`, b.RequestIDs[0]).Scan(&second)
	r.Store.db.Exec(`UPDATE representations SET state='waiting_provider' WHERE id=?`, first)
	r.Store.db.Exec(`INSERT INTO transfer_intents(representation_id,provider,candidate_id,state) VALUES(?,'fixture','rejected','rejected'),(?,'fixture','active','unresolved')`, first, second)
	r.Store.db.Exec(`UPDATE representations SET state='transferring' WHERE id=?`, second)
	lease, e := r.Store.Claim(ctx, "ebook", "owner", time.Now(), time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	in, _ := r.Store.input(ctx, lease.RequestID)
	if _, e = r.Store.Canonicalize(ctx, lease, in, Candidate{Title: "A", Provider: "fixture", ID: "same"}); e != nil {
		t.Fatal(e)
	}
	var state string
	r.Store.db.QueryRow(`SELECT state FROM representations WHERE id=?`, second).Scan(&state)
	owner, e := resolveJobID(ctx, r.Store.db, first)
	if e != nil || state != "transferring" || owner != second {
		t.Fatal("unresolved owner orphaned", state, owner, e)
	}
}
