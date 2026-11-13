package providers

import (
	"archive/zip"
	"github.com/joey12725/arrdio/backend/jobs"
	"os"
	"path/filepath"
	"testing"
)

func TestEPUBMetadataFollowsStablePackagePointer(t *testing.T) {
	path := filepath.Join(t.TempDir(), "book.epub")
	file, e := os.Create(path)
	if e != nil {
		t.Fatal(e)
	}
	z := zip.NewWriter(file)
	for name, text := range map[string]string{"META-INF/container.xml": `<container><rootfiles><rootfile full-path="OPS/real.opf"/></rootfiles></container>`, "wrong.opf": `<package><metadata><title>Unrelated Book</title></metadata></package>`, "OPS/real.opf": `<package xmlns:dc="http://purl.org/dc/elements/1.1/"><metadata><dc:title>The Jungle</dc:title><dc:creator>Upton Sinclair</dc:creator><dc:language>en</dc:language></metadata></package>`} {
		entry, _ := z.Create(name)
		entry.Write([]byte(text))
	}
	z.Close()
	file.Close()
	meta, e := ReadEPUBMetadata(path)
	if e != nil || meta.Title != "The Jungle" || meta.Author != "Upton Sinclair" {
		t.Fatal(meta, e)
	}
	if e = VerifyBookIdentity(path, jobs.Candidate{Title: "The Jungle", Author: "Upton Sinclair"}); e != nil {
		t.Fatal(e)
	}
	if e = VerifyBookIdentity(path, jobs.Candidate{Title: "The Jungle", Author: "Mark Twain"}); e == nil {
		t.Fatal("title collision with wrong author accepted")
	}
	if e = VerifyBookIdentity(path, jobs.Candidate{Title: "The Jungle", Author: "Upton Sinclair"}, jobs.Constraints{Language: "French"}); e == nil {
		t.Fatal("contradictory OPF language accepted")
	}
}
func TestHardRequirementsExcludeUnknownAndWrongCandidates(t *testing.T) {
	in := jobs.Input{Title: "A", Constraints: jobs.Constraints{Language: "English", Narrator: "Reader", Edition: "Unabridged", Container: "m4b"}}
	results := Rank(in, "audio", []jobs.Candidate{{ID: "unknown", Title: "A", Format: "audio", Extension: "m4b"}, {ID: "wrong", Title: "A", Format: "audio", Language: "English", Narrator: "Other", Edition: "Unabridged", Extension: "m4b"}, {ID: "right", Title: "A", Format: "audio", Language: "en", Narrator: "Reader", Edition: "Unabridged", Extension: "m4b"}})
	if len(results) != 1 || results[0].ID != "right" {
		t.Fatal(results)
	}
}
func TestSubtitleFallbackRequiresSameAuthor(t *testing.T) {
	in := jobs.Input{Title: "Walden: Life in the Woods", Author: "Henry Thoreau"}
	candidates := Rank(in, "ebook", []jobs.Candidate{{ID: "right", Title: "Walden", Author: "Henry Thoreau", Format: "ebook"}, {ID: "collision", Title: "Walden", Author: "Other Author", Format: "ebook"}})
	if len(candidates) != 1 || candidates[0].ID != "right" {
		t.Fatal(candidates)
	}
}

// The live La fontana de oro EPUB: Spanish on Z-Library, dc:language UND.
func TestUndeterminedEPUBLanguageDoesNotVetoSourceLabel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "book.epub")
	file, _ := os.Create(path)
	z := zip.NewWriter(file)
	for name, text := range map[string]string{"META-INF/container.xml": `<container><rootfiles><rootfile full-path="OPS/content.opf"/></rootfiles></container>`, "OPS/content.opf": `<package xmlns:dc="http://purl.org/dc/elements/1.1/"><metadata><dc:title>Fontana de Oro, La</dc:title><dc:creator>Pérez Galdós Benito</dc:creator><dc:language>UND</dc:language></metadata></package>`} {
		entry, _ := z.Create(name)
		entry.Write([]byte(text))
	}
	z.Close()
	file.Close()
	candidate := jobs.Candidate{Title: "La fontana de oro", Author: "Benito Pérez Galdós"}
	if e := VerifyBookIdentity(path, candidate, jobs.Constraints{Language: "Spanish"}); e != nil {
		t.Fatal(e)
	}
}
