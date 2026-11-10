package providers

import (
	"archive/zip"
	"bytes"
	"context"
	"github.com/ashkaty/arrdio-api/jobs"
	"os"
	"path/filepath"
	"testing"
)

func TestAvailabilityRankingAndKind(t *testing.T) {
	zero, live := 0, 12
	in := jobs.Input{Title: "The Jungle", Author: "Upton Sinclair"}
	results := Rank(in, "audio", []jobs.Candidate{{ID: "dead", Title: "The Jungle", Author: "Upton Sinclair", Format: "audio", Extension: "m4b", Seeders: &zero}, {ID: "live", Title: "The Jungle", Author: "Upton Sinclair", Format: "audio", Extension: "mp3", Seeders: &live}, {ID: "wrong", Title: "The Jungle", Author: "Mark Twain", Format: "audio", Seeders: &live}, {ID: "ebook", Title: "The Jungle", Format: "ebook", Seeders: &live}})
	if len(results) != 2 || results[0].ID != "live" {
		t.Fatal(results)
	}
}
func TestTrustedTransfers(t *testing.T) {
	for _, raw := range []string{"http://z-library.ec/book", "https://z-library.ec.attacker.invalid/book", "https://user:password@z-library.ec/book", "https://127.0.0.1/book", "https://z-library.ec:444/book"} {
		if trustedURL(raw, []string{"z-library.ec"}) {
			t.Fatal(raw)
		}
	}
	if !trustedURL("https://cdn.z-library.ec/book", []string{"z-library.ec"}) {
		t.Fatal("trusted CDN rejected")
	}
}
func TestPayloadValidationAndAtomicImport(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "login.epub")
	os.WriteFile(bad, []byte("<html>login</html>"), 0600)
	if Validate(bad) == nil {
		t.Fatal("HTML accepted")
	}
	var buffer bytes.Buffer
	z := zip.NewWriter(&buffer)
	m, _ := z.Create("mimetype")
	m.Write([]byte("application/epub+zip"))
	p, _ := z.Create("book.xhtml")
	p.Write([]byte("<html><body>Book fixture</body></html>"))
	z.Close()
	src := filepath.Join(dir, "book.epub")
	os.WriteFile(src, buffer.Bytes(), 0600)
	if e := Validate(src); e != nil {
		t.Fatal(e)
	}
	dst := filepath.Join(dir, "library.epub")
	if e := copyVerified(src, dst); e != nil {
		t.Fatal(e)
	}
	if e := copyVerified(src, dst); e != nil {
		t.Fatal("idempotent copy", e)
	}
	if _, e := os.Stat(src); e != nil {
		t.Fatal("source removed")
	}
	os.WriteFile(dst, []byte("collision"), 0600)
	if e := copyVerified(src, dst); e == nil {
		t.Fatal("overwrote collision")
	}
}
func TestJevUnavailableFallsBack(t *testing.T) {
	m := &Models{}
	if got := m.Decide(context.Background(), jobs.Input{Title: "A"}, []jobs.Candidate{{ID: "one"}, {ID: "two"}}); got != 0 || !m.Degraded {
		t.Fatal(got, m.Degraded)
	}
}
func TestCategoryDiscovery(t *testing.T) {
	cats := []Category{{ID: 1, Name: "TV/Anime"}, {ID: 2, Name: "Books", Children: []Category{{ID: 156719, Name: "Literature English-translated"}}}, {ID: 3030, Name: "Audio/Audiobook"}}
	got := categoryIDs(cats, "manga")
	if len(got) != 1 || got[0] != 156719 {
		t.Fatal(got)
	}
	got = categoryIDs(cats, "audio")
	if len(got) != 1 || got[0] != 3030 {
		t.Fatal(got)
	}
}
