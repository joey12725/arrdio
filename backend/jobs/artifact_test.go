package jobs

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestArtifactVerificationFencesFreshAndRecoveredImport(t *testing.T) {
	r := runtimeTest(t, filepath.Join(t.TempDir(), "jobs.db"))
	library := &fakeLibrary{}
	r.Library = library
	source := &effectSource{name: "fixture", r: r}
	r.Sources = []Source{source}
	enqueue(t, r.Store, "artifact", Input{Title: "A", Formats: []string{"ebook"}})
	checks := 0
	r.VerifyArtifact = func(context.Context, Input, Receipt) error {
		checks++
		return errors.New("contradictory artifact language")
	}
	r.step(ctx, "ebook")
	counts, _ := r.Store.Counts(ctx)
	if checks != 1 || library.imports != 0 || counts["permanent_failure"] != 1 {
		t.Fatal(checks, library.imports, counts)
	}
	r.Store.db.Exec(`UPDATE representations SET state='importing',eligible_at=0,lease_until=NULL,lease_token=NULL`)
	r.VerifyLocal = func(_ context.Context, _ string, paths []string) ([]string, error) { return paths, nil }
	r.resume(ctx, "ebook")
	if checks != 2 || library.imports != 0 || source.acquires != 1 {
		t.Fatal("recovery bypassed artifact verification", checks, library.imports, source.acquires)
	}
}
